// nolint: gosec
package kvstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sync "github.com/sasha-s/go-deadlock"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/ds"
)

const (
	snapshotChunkSize = 1e6

	// Keep only the most recent 10 snapshots. Older snapshots are pruned
	maxSnapshotCount = 10
)

// SnapshotStore stores state sync snapshots. Snapshots are stored simply as
// JSON files, and chunks are generated on-the-fly by splitting the JSON data
// into fixed-size chunks.
type SnapshotStore struct {
	sync.RWMutex
	dir      string
	metadata []abci.Snapshot
}

// NewSnapshotStore creates a new snapshot store.
func NewSnapshotStore(dir string) (*SnapshotStore, error) {
	store := &SnapshotStore{dir: dir}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := store.loadMetadata(); err != nil {
		return nil, err
	}
	return store, nil
}

// loadMetadata loads snapshot metadata. Does not take out locks, since it's
// called internally on construction.
func (s *SnapshotStore) loadMetadata() error {
	file := filepath.Join(s.dir, "metadata.json")
	metadata := []abci.Snapshot{}

	bz, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("failed to load snapshot metadata from %q: %w", file, err)
	}
	if len(bz) != 0 {
		err = json.Unmarshal(bz, &metadata)
		if err != nil {
			return fmt.Errorf("invalid snapshot data in %q: %w", file, err)
		}
	}
	s.metadata = metadata
	return nil
}

// saveMetadata saves snapshot metadata. Does not take out locks, since it's
// called internally from e.g. Create().
func (s *SnapshotStore) saveMetadata() error {
	bz, err := json.Marshal(s.metadata)
	if err != nil {
		return err
	}

	// save the file to a new file and move it to make saving atomic.
	newFile := filepath.Join(s.dir, "metadata.json.new")
	file := filepath.Join(s.dir, "metadata.json")
	err = os.WriteFile(newFile, bz, 0644) // nolint: gosec
	if err != nil {
		return err
	}
	return os.Rename(newFile, file)
}

// Create creates a snapshot of the given application state's key/value pairs.
func (s *SnapshotStore) Create(state State) (abci.Snapshot, error) {
	s.Lock()
	defer s.Unlock()

	bz, err := json.Marshal(state)
	if err != nil {
		return abci.Snapshot{}, err
	}
	height := state.GetHeight()
	snapshot := abci.Snapshot{
		Height:  uint64(height),
		Version: 1,
		Hash:    crypto.Checksum(bz),
	}
	err = os.WriteFile(filepath.Join(s.dir, fmt.Sprintf("%v.json", height)), bz, 0644)
	if err != nil {
		return abci.Snapshot{}, err
	}
	s.metadata = append(s.metadata, snapshot)
	err = s.saveMetadata()
	if err != nil {
		return abci.Snapshot{}, err
	}
	return snapshot, nil
}

// Prune removes old snapshots ensuring only the most recent n snapshots remain
func (s *SnapshotStore) Prune(n int) error {
	s.Lock()
	defer s.Unlock()
	// snapshots are appended to the metadata struct, hence pruning removes from
	// the front of the array
	i := 0
	for ; i < len(s.metadata)-n; i++ {
		h := s.metadata[i].Height
		path := filepath.Join(s.dir, fmt.Sprintf("%v.json", h))
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		err = os.Remove(path)
		if err != nil {
			return err
		}
	}

	// update metadata by removing the deleted snapshots
	pruned := make([]abci.Snapshot, len(s.metadata[i:]))
	copy(pruned, s.metadata[i:])
	s.metadata = pruned
	return nil
}

// List lists available snapshots.
func (s *SnapshotStore) List() ([]*abci.Snapshot, error) {
	s.RLock()
	defer s.RUnlock()
	snapshots := make([]*abci.Snapshot, len(s.metadata))
	for idx := range s.metadata {
		snapshots[idx] = &s.metadata[idx]
	}
	return snapshots, nil
}

// LoadChunk loads a snapshot chunk.
func (s *SnapshotStore) LoadChunk(height uint64, version uint32, chunkID []byte) ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	for _, snapshot := range s.metadata {
		if snapshot.Height == height && snapshot.Version == version {
			bz, err := os.ReadFile(filepath.Join(s.dir, fmt.Sprintf("%v.json", height)))
			if err != nil {
				return nil, err
			}
			return byteChunk(bz, chunkID), nil
		}
	}
	return nil, nil
}

type offerSnapshot struct {
	snapshot *abci.Snapshot
	appHash  tmbytes.HexBytes
	chunks   *ds.OrderedMap[string, []byte]
}

func newOfferSnapshot(snapshot *abci.Snapshot, appHash tmbytes.HexBytes) *offerSnapshot {
	return &offerSnapshot{
		snapshot: snapshot,
		appHash:  appHash,
		chunks:   ds.NewOrderedMap[string, []byte](),
	}
}

func (s *offerSnapshot) addChunk(chunkID tmbytes.HexBytes, chunk []byte) {
	if s.chunks.Has(chunkID.String()) {
		return
	}
	s.chunks.Put(chunkID.String(), chunk)
}

func (s *offerSnapshot) isFull() bool {
	return bytes.Equal(crypto.Checksum(s.bytes()), s.snapshot.Hash)
}

func (s *offerSnapshot) bytes() []byte {
	chunks := s.chunks.Values()
	buf := bytes.NewBuffer(nil)
	for _, chunk := range chunks {
		buf.Write(chunk)
	}
	return buf.Bytes()
}

// byteChunk returns the chunk at a given index from the full byte slice.
func byteChunk(bz []byte, chunkID []byte) []byte {
	for i := 0; i < len(bz); i += snapshotChunkSize {
		j := i + snapshotChunkSize
		if j > len(bz) {
			j = len(bz)
		}
		key := crypto.Checksum(bz[i:j])
		if bytes.Equal(key, chunkID) {
			return append([]byte(nil), bz[i:j]...)
		}
	}
	return bz[:snapshotChunkSize]
}
