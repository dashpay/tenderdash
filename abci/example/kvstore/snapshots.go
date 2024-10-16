package kvstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sync "github.com/sasha-s/go-deadlock"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/ds"
	tmmath "github.com/dashpay/tenderdash/libs/math"
)

const (
	snapshotChunkSize = 1e6

	// Keep only the most recent 10 snapshots. Older snapshots are pruned
	maxSnapshotCount = 10
)

// SnapshotStore stores state sync snapshots. Snapshots are stored simply as
// JSON files, and chunks are generated on-the-fly by splitting the JSON data
// into fixed-size chunks.
type (
	SnapshotStore struct {
		sync.RWMutex
		dir      string
		metadata []abci.Snapshot
	}
	chunkItem struct {
		Data         []byte   `json:"data"`
		NextChunkIDs [][]byte `json:"nextChunkIDs"`
	}
)

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
	var metadata []abci.Snapshot

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
	err = os.WriteFile(newFile, bz, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	return os.Rename(newFile, file)
}

// Create creates a snapshot of the given application state's key/value pairs.
func (s *SnapshotStore) Create(state State) (abci.Snapshot, error) {
	s.Lock()
	defer s.Unlock()

	height := state.GetHeight()

	filename := filepath.Join(s.dir, fmt.Sprintf("%v.json", height))
	f, err := os.Create(filename)
	if err != nil {
		return abci.Snapshot{}, err
	}
	defer f.Close()

	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)

	if err := state.Save(writer); err != nil {
		f.Close()
		// Cleanup incomplete file; ignore errors during cleanup
		_ = os.Remove(filename)
		return abci.Snapshot{}, err
	}

	snapshot := abci.Snapshot{
		Height:  tmmath.MustConvertUint64(height),
		Version: 1,
		Hash:    hasher.Sum(nil),
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
			bz, err := os.ReadFile(filepath.Join(s.dir, fmt.Sprintf("%d.json", height)))
			if err != nil {
				return nil, err
			}
			chunks := makeChunks(bz, snapshotChunkSize)
			item := makeChunkItem(chunks, chunkID)
			return json.Marshal(item)
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

func (s *offerSnapshot) addChunk(chunkID tmbytes.HexBytes, data []byte) [][]byte {
	chunkIDStr := chunkID.String()
	if s.chunks.Has(chunkIDStr) {
		return nil
	}
	var item chunkItem
	err := json.Unmarshal(data, &item)
	if err != nil {
		panic("failed to decode a chunk data: " + err.Error())
	}
	s.chunks.Put(chunkIDStr, item.Data)
	return item.NextChunkIDs
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

// reader returns a reader for the snapshot data.
func (s *offerSnapshot) reader() io.ReadCloser {
	chunks := s.chunks.Values()
	reader := &chunkedReader{chunks: chunks}

	return reader
}

type chunkedReader struct {
	chunks [][]byte
	index  int
	offset int
}

func (r *chunkedReader) Read(p []byte) (n int, err error) {
	if r.chunks == nil {
		return 0, io.EOF
	}
	for n < len(p) && r.index < len(r.chunks) {
		copyCount := copy(p[n:], r.chunks[r.index][r.offset:])
		n += copyCount
		r.offset += copyCount
		if r.offset >= len(r.chunks[r.index]) {
			r.index++
			r.offset = 0
		}
	}
	if r.index >= len(r.chunks) {
		err = io.EOF
	}
	return
}

func (r *chunkedReader) Close() error {
	r.chunks = nil
	return nil
}

// makeChunkItem returns the chunk at a given index from the full byte slice.
func makeChunkItem(chunks *ds.OrderedMap[string, []byte], chunkID []byte) chunkItem {
	chunkIDStr := hex.EncodeToString(chunkID)
	val, ok := chunks.Get(chunkIDStr)
	if !ok {
		panic("chunk not found")
	}
	chunkIDs := chunks.Keys()
	ci := chunkItem{Data: val}
	i := 0
	for ; i < len(chunkIDs) && chunkIDs[i] != chunkIDStr; i++ {
	}
	if i+1 < len(chunkIDs) {
		data, err := hex.DecodeString(chunkIDs[i+1])
		if err != nil {
			panic(err)
		}
		ci.NextChunkIDs = [][]byte{data}
	}
	return ci
}

func makeChunks(bz []byte, chunkSize int) *ds.OrderedMap[string, []byte] {
	chunks := ds.NewOrderedMap[string, []byte]()
	totalHash := hex.EncodeToString(crypto.Checksum(bz))
	key := totalHash
	for i := 0; i < len(bz); i += chunkSize {
		j := i + chunkSize
		if j > len(bz) {
			j = len(bz)
		}
		if i > 1 {
			key = hex.EncodeToString(crypto.Checksum(bz[i:j]))
		}
		chunks.Put(key, append([]byte(nil), bz[i:j]...))
	}
	return chunks
}
