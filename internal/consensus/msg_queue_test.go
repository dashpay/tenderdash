package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

func TestChanQueue(t *testing.T) {
	ctx := context.Background()
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	queue := newChanQueue[Message]()
	require.Equal(t, msgQueueSize, cap(queue.ch)) // by default, the queue channel must have a long buffer size
	testCases := []struct {
		ctx        context.Context
		bufferSize int
		wantErr    string
	}{
		{
			ctx:        context.Background(),
			bufferSize: 1,
			wantErr:    "",
		},
		{
			ctx:        canceledCtx,
			bufferSize: 1,
			wantErr:    "context canceled",
		},
		{
			ctx:        ctx,
			bufferSize: 0,
			wantErr:    "msg queue is full",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			var msg Message
			queue := newChanQueue[Message]()
			queue.ch = make(chan Message, tc.bufferSize)
			err := queue.send(tc.ctx, msg)
			if err != nil {
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.Equal(t, "", tc.wantErr)
		})
	}
}

func TestChanMsgSender(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		ctx             context.Context
		peerID          string
		wantInternalLen int
		wantPeerLen     int
	}{
		{
			ctx:             ctx,
			wantInternalLen: 1,
			wantPeerLen:     0,
		},
		{
			ctx:             ctxWithPeerQueue(ctx), // add a parameter into context to use peer queue
			wantInternalLen: 0,
			wantPeerLen:     1,
		},
		{
			ctx:             ctx,
			peerID:          "peer",
			wantInternalLen: 0,
			wantPeerLen:     1,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			sender := chanMsgSender{
				internalQueue: newChanQueue[msgInfo](),
				peerQueue:     newChanQueue[msgInfo](),
			}
			var mi Message
			err := sender.send(tc.ctx, mi, types.NodeID(tc.peerID))
			require.NoError(t, err)
			require.Len(t, sender.internalQueue.ch, tc.wantInternalLen)
			require.Len(t, sender.peerQueue.ch, tc.wantPeerLen)
		})
	}
}

func TestChanMsgReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue1 := newChanQueue[Message]()
	queue2 := newChanQueue[Message]()
	queue3 := newChanQueue[Message]()
	queues := []*chanQueue[Message]{queue1, queue2, queue3}
	var msg Message
	testCases := []struct {
		queues []*chanQueue[Message]
		n      int
	}{
		{
			queues: []*chanQueue[Message]{queue1, queue2, queue3},
			n:      10,
		},
		{
			queues: []*chanQueue[Message]{queue1},
			n:      3,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			var (
				actualCnt int
				stoppedCh = make(chan struct{})
			)
			reader := newChanMsgReader[Message](queues)
			go reader.fanIn(ctx)
			go func(n int) {
				for i := 0; i < n; i++ {
					<-reader.outCh
					actualCnt++
				}
				stoppedCh <- struct{}{}
			}(tc.n)
			l := len(tc.queues)
			for j := 0; j < tc.n; j++ {
				p := j % l
				err := tc.queues[p].send(ctx, msg)
				require.NoError(t, err)
			}
			<-stoppedCh
			close(stoppedCh)
			reader.stop()
			require.Equal(t, tc.n, actualCnt)
			for _, queue := range queues {
				require.Len(t, queue.ch, 0)
			}
		})
	}
}
