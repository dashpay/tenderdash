package statesync

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/bytes"
)

type ChunkQueueTestSuite struct {
	suite.Suite

	snapshot *snapshot
	queue    *chunkQueue
	tempDir  string
	chunks   []*chunk
}

func TestChunkQueue(t *testing.T) {
	suite.Run(t, new(ChunkQueueTestSuite))
}

func (suite *ChunkQueueTestSuite) SetupSuite() {
	suite.snapshot = &snapshot{
		Height:   3,
		Version:  1,
		Hash:     []byte{0},
		Metadata: nil,
	}
	suite.chunks = []*chunk{
		{
			Height:  3,
			Version: 1,
			ID:      []byte{0},
			Chunk:   []byte{3, 1, 0},
			Sender:  "a",
		},
		{
			Height:  3,
			Version: 1,
			ID:      []byte{1},
			Chunk:   []byte{3, 1, 1},
			Sender:  "b",
		},
		{
			Height:  3,
			Version: 1,
			ID:      []byte{2},
			Chunk:   []byte{3, 1, 2},
			Sender:  "c",
		},
		{
			Height:  3,
			Version: 1,
			ID:      []byte{3},
			Chunk:   []byte{3, 1, 3},
			Sender:  "d",
		},
	}
}

func (suite *ChunkQueueTestSuite) SetupTest() {
	var err error
	suite.tempDir = suite.T().TempDir()
	suite.queue, err = newChunkQueue(suite.snapshot, suite.tempDir, 100)
	suite.Require().NoError(err)
}

func (suite *ChunkQueueTestSuite) TearDownTest() {
	err := suite.queue.Close()
	suite.Require().NoError(err)
}

func (suite *ChunkQueueTestSuite) TestTempDir() {
	files, err := os.ReadDir(suite.tempDir)
	suite.Require().NoError(err)
	suite.Require().Len(files, 1)

	err = suite.queue.Close()
	suite.Require().NoError(err)

	files, err = os.ReadDir(suite.tempDir)
	suite.Require().NoError(err)
	suite.Require().Len(files, 0)
}

func (suite *ChunkQueueTestSuite) TestChunkQueue() {
	suite.initChunks()
	testCases := []struct {
		chunk *chunk
		want  bool
	}{
		{chunk: suite.chunks[0], want: true},
		{chunk: suite.chunks[2], want: true},
		{chunk: suite.chunks[0], want: false},
		{chunk: suite.chunks[2], want: false},
		{chunk: suite.chunks[1], want: true},
	}
	require := suite.Require()
	for i, tc := range testCases {
		added, err := suite.queue.Add(tc.chunk)
		require.NoError(err, "test case %d", i)
		require.Equal(tc.want, added, "test case %d", i)
	}

	// At this point, we should be able to retrieve them all via Next
	for _, i := range []int{0, 2, 1} {
		c, err := suite.queue.Next()
		require.NoError(err)
		require.Equal(suite.chunks[i], c)
	}

	// It should still be possible to try to add chunks (which will be ignored)
	added, err := suite.queue.Add(suite.chunks[0])
	require.NoError(err)
	require.False(added)

	// After closing the requestQueue it will also return false
	err = suite.queue.Close()
	require.NoError(err)
	added, err = suite.queue.Add(suite.chunks[0])
	require.Error(err, errNilSnapshot)
	require.False(added)

	// Closing the queue again should also be fine
	err = suite.queue.Close()
	require.NoError(err)
}

func (suite *ChunkQueueTestSuite) TestAddChunkErrors() {
	testCases := map[string]struct {
		chunk *chunk
	}{
		"nil chunk":     {nil},
		"nil body":      {&chunk{Height: 3, Version: 1, ID: []byte{1}, Chunk: nil}},
		"wrong height":  {&chunk{Height: 9, Version: 1, ID: []byte{2}, Chunk: []byte{2}}},
		"wrong version": {&chunk{Height: 3, Version: 9, ID: []byte{3}, Chunk: []byte{3}}},
		"invalid index": {&chunk{Height: 3, Version: 1, ID: []byte{4}, Chunk: []byte{4}}},
	}
	for name, tc := range testCases {
		suite.Run(name, func() {
			_, err := suite.queue.Add(tc.chunk)
			suite.Require().Error(err)
		})
	}
}

func (suite *ChunkQueueTestSuite) TestDiscard() {
	suite.initChunks()
	require := suite.Require()
	// Add a few chunks to the queue and fetch a couple
	for _, c := range suite.chunks {
		_, err := suite.queue.Add(c)
		require.NoError(err)
	}
	for _, i := range []int{0, 1} {
		c, err := suite.queue.Next()
		require.NoError(err)
		require.EqualValues(suite.chunks[i].ID, c.ID)
	}
	// Discarding the first chunk and re-adding it should cause it to be returned
	// immediately by Next(), before proceeding with chunk 2
	err := suite.queue.Discard(suite.chunks[0].ID)
	require.NoError(err)
	added, err := suite.queue.Add(suite.chunks[0])
	require.NoError(err)
	require.True(added)
	nextChunk, err := suite.queue.Next()
	require.NoError(err)
	require.EqualValues(suite.chunks[2].ID, nextChunk.ID)

	// Discarding a non-existent chunk does nothing.
	err = suite.queue.Discard(factory.RandomHash())
	require.NoError(err)

	// When discard a couple of chunks, we should be able to allocate, add, and fetch them again.
	for _, i := range []int{1, 2} {
		err = suite.queue.Discard(suite.chunks[i].ID)
		require.NoError(err)
	}

	for _, i := range []int{2, 1} {
		added, err = suite.queue.Add(suite.chunks[i])
		require.NoError(err)
		require.True(added)
	}

	for _, i := range []int{3, 0, 2, 1} {
		nextChunk, err = suite.queue.Next()
		require.NoError(err)
		require.EqualValues(suite.chunks[i].ID, nextChunk.ID)
	}

	// After closing the requestQueue, discarding does nothing
	err = suite.queue.Close()
	require.NoError(err)
	err = suite.queue.Discard(suite.chunks[2].ID)
	require.NoError(err)
}

func (suite *ChunkQueueTestSuite) TestDiscardSender() {
	suite.initChunks()
	suite.processChunks()

	// Discarding an unknown sender should do nothing
	err := suite.queue.DiscardSender("unknown")
	suite.Require().NoError(err)

	// Discarding sender b should discard chunk 4, but not chunk 1 which has already been
	// returned.
	err = suite.queue.DiscardSender(suite.chunks[1].Sender)
	suite.Require().NoError(err)
	suite.Require().True(suite.queue.IsRequestQueueEmpty())
}

func (suite *ChunkQueueTestSuite) TestGetSender() {
	suite.initChunks()
	require := suite.Require()
	_, err := suite.queue.Add(suite.chunks[0])
	require.NoError(err)
	_, err = suite.queue.Add(suite.chunks[1])
	require.NoError(err)

	require.EqualValues(suite.chunks[0].Sender, suite.queue.GetSender(suite.chunks[0].ID))
	require.EqualValues(suite.chunks[1].Sender, suite.queue.GetSender(suite.chunks[1].ID))
	require.EqualValues("", suite.queue.GetSender(suite.chunks[2].ID))

	// After the chunk has been processed, we should still know who the sender was
	nextChunk, err := suite.queue.Next()
	require.NoError(err)
	require.NotNil(nextChunk)
	require.EqualValues(suite.chunks[0].ID, nextChunk.ID)
	require.EqualValues(suite.chunks[0].Sender, suite.queue.GetSender(suite.chunks[0].ID))
}

func (suite *ChunkQueueTestSuite) TestNext() {
	suite.initChunks()
	require := suite.Require()

	type chunkResult struct {
		chunk *chunk
		err   error
	}

	results := make(chan chunkResult, len(suite.chunks)+1)
	go func() {
		for {
			c, err := suite.queue.Next()
			results <- chunkResult{chunk: c, err: err}
			if errors.Is(err, errDone) {
				close(results)
				return
			}
		}
	}()

	select {
	case res := <-results:
		suite.T().Fatalf("unexpected chunk before any were added: chunk=%v err=%v", res.chunk, res.err)
	case <-time.After(10 * time.Millisecond):
	}

	_, err := suite.queue.Add(suite.chunks[1])
	require.NoError(err)
	var res chunkResult
	select {
	case res = <-results:
	case <-time.After(time.Second):
		suite.T().Fatal("timed out waiting for first chunk")
	}
	require.NoError(res.err)
	require.Equal(suite.chunks[1], res.chunk)

	_, err = suite.queue.Add(suite.chunks[0])
	require.NoError(err)
	select {
	case res = <-results:
	case <-time.After(time.Second):
		suite.T().Fatal("timed out waiting for second chunk")
	}
	require.NoError(res.err)
	require.Equal(suite.chunks[0], res.chunk)

	err = suite.queue.Close()
	require.NoError(err)
	select {
	case res, ok := <-results:
		require.True(ok)
		require.ErrorIs(res.err, errDone)
		require.Nil(res.chunk)
	case <-time.After(time.Second):
		suite.T().Fatal("timed out waiting for queue close notification")
	}

	_, ok := <-results
	require.False(ok)
}

func (suite *ChunkQueueTestSuite) TestNextClosed() {
	suite.initChunks()
	require := suite.Require()
	// Calling Next on a closed queue should return done
	_, err := suite.queue.Add(suite.chunks[1])
	require.NoError(err)
	err = suite.queue.Close()
	require.NoError(err)

	_, err = suite.queue.Next()
	require.ErrorIs(err, errDone)
}

func (suite *ChunkQueueTestSuite) TestRetry() {
	suite.initChunks()
	suite.processChunks()
	require := suite.Require()

	for i := range []int{2, 0} {
		suite.queue.Retry(suite.chunks[i].ID)
		chunkID, err := suite.queue.Dequeue()
		require.NoError(err)
		require.Equal(chunkID, suite.chunks[i].ID)
	}
}

func (suite *ChunkQueueTestSuite) TestRetryAll() {
	suite.initChunks()
	suite.processChunks()
	require := suite.Require()
	require.True(suite.queue.IsRequestQueueEmpty())
	suite.queue.RetryAll()
	require.Equal(len(suite.chunks), suite.queue.RequestQueueLen())
}

func (suite *ChunkQueueTestSuite) TestWaitFor() {
	suite.initChunks()
	require := suite.Require()
	waitForChs := make([]<-chan bytes.HexBytes, len(suite.chunks))
	for i, c := range suite.chunks {
		waitForChs[i] = suite.queue.WaitFor(c.ID)
	}

	for _, ch := range waitForChs {
		select {
		case <-ch:
			require.Fail("WaitFor should not trigger")
		default:
		}
	}

	_, err := suite.queue.Add(suite.chunks[0])
	require.NoError(err)
	require.EqualValues(suite.chunks[0].ID, <-waitForChs[0])
	_, ok := <-waitForChs[0]
	require.False(ok)

	// Fetch the first chunk. At this point, waiting for either 0 (retrieved from pool) or 1
	// (queued in pool) should immediately return true.
	c, err := suite.queue.Next()
	require.NoError(err)
	require.EqualValues(suite.chunks[0].ID, c.ID)

	// Close the queue. This should cause the waiter for 4 to close, and also cause any future
	// waiters to get closed channels.
	err = suite.queue.Close()
	require.NoError(err)
	_, ok = <-waitForChs[2]
	require.False(ok)
}

func (suite *ChunkQueueTestSuite) initChunks() {
	for _, c0 := range suite.chunks {
		suite.queue.Enqueue(c0.ID)
		c1, err := suite.queue.Dequeue()
		suite.Require().NoError(err)
		suite.Require().Equal(c0.ID, c1)
	}
}

func (suite *ChunkQueueTestSuite) processChunks() {
	for _, c := range suite.chunks {
		added, err := suite.queue.Add(c)
		suite.Require().NoError(err)
		suite.Require().True(added)
		c1, err := suite.queue.Next()
		suite.Require().NoError(err)
		suite.Require().Equal(c, c1)
	}
}
