package statesync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

// errDone is returned by chunkQueue.Next() when all chunks have been returned.
var (
	errDone        = errors.New("chunk queue has completed")
	errQueueEmpty  = errors.New("requestQueue is empty")
	errChunkNil    = errors.New("cannot add nil chunk")
	errNoChunkItem = errors.New("no chunk item found")
	errNilSnapshot = errors.New("snapshot is nil")
)

const (
	initStatus chunkStatus = iota
	inProgressStatus
	discardedStatus
	receivedStatus
	doneStatus
)

// chunk contains data for a chunk.
type (
	chunk struct {
		Height  uint64
		Version uint32
		ID      bytes.HexBytes
		Chunk   []byte
		Sender  types.NodeID
	}
	chunkStatus int
	chunkItem   struct {
		chunkID bytes.HexBytes
		file    string                  // path to temporary chunk file
		sender  types.NodeID            // the peer who sent the given chunk
		waitChs []chan<- bytes.HexBytes // signals WaitFor() waiters about chunk arrival
		status  chunkStatus             // status of the chunk
	}
	// chunkQueue manages chunks for a state sync process, ordering them if requested. It acts as an
	// iterator over all chunks, but callers can request chunks to be retried, optionally after
	// refetching.
	chunkQueue struct {
		mtx          sync.Mutex
		snapshot     *snapshot // if this is nil, the queue has been closed
		dir          string    // temp dir for on-disk chunk storage
		items        map[string]*chunkItem
		requestQueue []bytes.HexBytes
		applyCh      chan bytes.HexBytes
		// doneCount counts the number of chunks that have been processed to the done status
		// if for some reason some chunks have been processed more than once, this number should take them into account
		doneCount int
	}
)

// newChunkQueue creates a new chunk requestQueue for a snapshot, using a temp dir for storage.
// Callers must call Close() when done.
func newChunkQueue(snapshot *snapshot, tempDir string, bufLen int) (*chunkQueue, error) {
	dir, err := os.MkdirTemp(tempDir, "tm-statesync")
	if err != nil {
		return nil, fmt.Errorf("unable to create temp dir for state sync chunks: %w", err)
	}
	if snapshot.Hash.IsZero() {
		return nil, errors.New("snapshot has no chunks")
	}
	return &chunkQueue{
		snapshot: snapshot,
		dir:      dir,
		items:    make(map[string]*chunkItem),
		applyCh:  make(chan bytes.HexBytes, bufLen),
	}, nil
}

// IsRequestQueueEmpty returns true if the request queue is empty
func (q *chunkQueue) IsRequestQueueEmpty() bool {
	return q.RequestQueueLen() == 0
}

// RequestQueueLen returns the length of the request queue
func (q *chunkQueue) RequestQueueLen() int {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	return len(q.requestQueue)
}

// Enqueue adds a chunk ID to the end of the requestQueue
func (q *chunkQueue) Enqueue(chunkIDs ...[]byte) {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	for _, chunkID := range chunkIDs {
		q.enqueue(chunkID)
	}
}

func (q *chunkQueue) enqueue(chunkID bytes.HexBytes) {
	q.requestQueue = append(q.requestQueue, chunkID)
	_, ok := q.items[chunkID.String()]
	if ok {
		return
	}
	q.items[chunkID.String()] = &chunkItem{
		chunkID: chunkID,
		status:  initStatus,
	}
}

// Dequeue returns the next chunk ID in the requestQueue, or an error if the queue is empty
func (q *chunkQueue) Dequeue() (bytes.HexBytes, error) {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	return q.dequeue()
}

func (q *chunkQueue) dequeue() (bytes.HexBytes, error) {
	if len(q.requestQueue) == 0 {
		return nil, errQueueEmpty
	}
	chunkID := q.requestQueue[0]
	q.requestQueue = q.requestQueue[1:]
	q.items[chunkID.String()].status = inProgressStatus
	return chunkID, nil
}

// Add adds a chunk to the queue. It ignores chunks that already exist, returning false.
func (q *chunkQueue) Add(chunk *chunk) (bool, error) {
	if chunk == nil || chunk.Chunk == nil {
		return false, errChunkNil
	}
	q.mtx.Lock()
	defer q.mtx.Unlock()
	if q.snapshot == nil {
		return false, errNilSnapshot
	}
	chunkIDKey := chunk.ID.String()
	item, ok := q.items[chunkIDKey]
	if !ok {
		return false, fmt.Errorf("failed to add the chunk %x, it was never requested", chunk.ID)
	}
	if item.status != inProgressStatus && item.status != discardedStatus {
		return false, nil
	}
	err := q.validateChunk(chunk)
	if err != nil {
		return false, err
	}
	item.file = filepath.Join(q.dir, chunkIDKey)
	err = item.write(chunk.Chunk)
	if err != nil {
		return false, err
	}
	item.sender = chunk.Sender
	item.status = receivedStatus
	q.applyCh <- chunk.ID
	// Signal any waiters that the chunk has arrived.
	item.closeWaitChs(true)
	return true, nil
}

// Close closes the chunk queue, cleaning up all temporary files.
func (q *chunkQueue) Close() error {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	if q.snapshot == nil {
		return nil
	}
	q.snapshot = nil
	close(q.applyCh)
	for len(q.applyCh) > 0 {
		<-q.applyCh
	}
	for _, item := range q.items {
		item.closeWaitChs(false)
	}
	if err := os.RemoveAll(q.dir); err != nil {
		return fmt.Errorf("failed to clean up state sync tempdir %s: %w", q.dir, err)
	}
	return nil
}

// Discard discards a chunk. It will be removed from the queue, available for allocation, and can
// be added and returned via Next() again. If the chunk is not already in the queue this does
// nothing, to avoid it being allocated to multiple fetchers.
func (q *chunkQueue) Discard(chunkID bytes.HexBytes) error {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	return q.discard(chunkID)
}

// discard discards a chunk, scheduling it for refetching. The caller must hold the mutex lock.
func (q *chunkQueue) discard(chunkID bytes.HexBytes) error {
	if q.snapshot == nil {
		return nil
	}
	chunkIDKey := chunkID.String()
	item, ok := q.items[chunkIDKey]
	if !ok {
		return nil
	}
	item.status = discardedStatus
	return item.remove()
}

// DiscardSender discards all *unreturned* chunks from a given sender. If the caller wants to
// discard already returned chunks, this can be done via Discard().
func (q *chunkQueue) DiscardSender(peerID types.NodeID) error {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	for _, item := range q.items {
		if item.sender == peerID && item.isDiscardable() {
			err := q.discard(item.chunkID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// GetSender returns the sender of the chunk with the given index, or empty if
// not found.
func (q *chunkQueue) GetSender(chunkID bytes.HexBytes) types.NodeID {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	item, ok := q.items[chunkID.String()]
	if ok {
		return item.sender
	}
	return ""
}

// load loads a chunk from disk, or nil if the chunk is not in the queue. The caller must hold the
// mutex lock.
func (q *chunkQueue) load(chunkID bytes.HexBytes) (*chunk, error) {
	chunkIDKey := chunkID.String()
	item, ok := q.items[chunkIDKey]
	if !ok {
		return nil, errNoChunkItem
	}
	if item.status != receivedStatus {
		return nil, nil
	}
	data, err := item.loadData()
	if err != nil {
		return nil, err
	}
	return &chunk{
		Height:  q.snapshot.Height,
		Version: q.snapshot.Version,
		ID:      chunkID,
		Chunk:   data,
		Sender:  item.sender,
	}, nil
}

// Next returns the next chunk from the queue, or errDone if all chunks have been returned. It
// blocks until the chunk is available. Concurrent Next() calls may return the same chunk.
func (q *chunkQueue) Next() (*chunk, error) {
	select {
	case chunkID, ok := <-q.applyCh:
		if !ok {
			return nil, errDone // queue closed
		}
		q.mtx.Lock()
		defer q.mtx.Unlock()
		loadedChunk, err := q.load(chunkID)
		if err != nil {
			return nil, err
		}
		item, ok := q.items[chunkID.String()]
		if !ok {
			return nil, errNoChunkItem
		}
		item.status = doneStatus
		q.doneCount++
		return loadedChunk, nil
	case <-time.After(chunkTimeout):
		return nil, errTimeout
	}
}

// Retry schedules a chunk to be retried, without refetching it.
func (q *chunkQueue) Retry(chunkID bytes.HexBytes) {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	q.retry(chunkID)
}

func (q *chunkQueue) retry(chunkID bytes.HexBytes) {
	item, ok := q.items[chunkID.String()]
	if !ok || (item.status != receivedStatus && item.status != doneStatus) {
		return
	}
	q.requestQueue = append(q.requestQueue, chunkID)
	q.items[chunkID.String()].status = initStatus
}

// RetryAll schedules all chunks to be retried, without refetching them.
func (q *chunkQueue) RetryAll() {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	q.requestQueue = make([]bytes.HexBytes, 0, len(q.items))
	for _, item := range q.items {
		q.retry(item.chunkID)
	}
}

// WaitFor returns a channel that receives a chunk ID when it arrives in the queue, or
// immediately if it has already arrived. The channel is closed without a value if the queue is closed
func (q *chunkQueue) WaitFor(chunkID bytes.HexBytes) <-chan bytes.HexBytes {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	return q.waitFor(chunkID)
}

func (q *chunkQueue) waitFor(chunkID bytes.HexBytes) <-chan bytes.HexBytes {
	ch := make(chan bytes.HexBytes, 1)
	if q.snapshot == nil {
		close(ch)
		return ch
	}
	item, ok := q.items[chunkID.String()]
	if !ok {
		ch <- chunkID
		close(ch)
		return ch
	}
	item.waitChs = append(item.waitChs, ch)
	return ch
}

// DoneChunksCount returns the number of chunks that have been returned
func (q *chunkQueue) DoneChunksCount() int {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	return q.doneCount
}

func (q *chunkQueue) validateChunk(chunk *chunk) error {
	if chunk.Height != q.snapshot.Height {
		return fmt.Errorf("invalid chunk height %v, expected %v",
			chunk.Height,
			q.snapshot.Height)
	}
	if chunk.Version != q.snapshot.Version {
		return fmt.Errorf("invalid chunk version %v, expected %v",
			chunk.Version,
			q.snapshot.Version)
	}
	return nil
}

func (c *chunkItem) remove() error {
	if err := os.Remove(c.file); err != nil {
		return fmt.Errorf("failed to remove chunk %s: %w", c.chunkID, err)
	}
	c.file = ""
	return nil
}

func (c *chunkItem) write(data []byte) error {
	err := os.WriteFile(c.file, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to save chunk %v to file %v: %w", c.chunkID, c.file, err)
	}
	return nil
}

func (c *chunkItem) loadData() ([]byte, error) {
	body, err := os.ReadFile(c.file)
	if err != nil {
		return nil, fmt.Errorf("failed to load chunk %s: %w", c.chunkID, err)
	}
	return body, nil
}

func (c *chunkItem) closeWaitChs(send bool) {
	for _, ch := range c.waitChs {
		if send {
			ch <- c.chunkID
		}
		close(ch)
	}
	c.waitChs = nil
}

// isDiscardable returns true if a status is suitable for transition to discarded, otherwise false
func (c *chunkItem) isDiscardable() bool {
	return c.status == initStatus
}
