package p2p

import (
	"context"
	"testing"
	"time"

	gogotypes "github.com/cosmos/gogoproto/types"

	"github.com/dashpay/tenderdash/libs/log"
)

type testMessage = gogotypes.StringValue

func TestCloseWhileDequeueFull(t *testing.T) {
	enqueueLength := 5
	chDescs := map[ChannelID]*ChannelDescriptor{
		0x01: {ID: 0x01, Priority: 1},
	}
	pqueue := newPQScheduler(log.NewNopLogger(), NopMetrics(), newMetricsLabelCache(), chDescs, uint(enqueueLength), 1, 120)

	for i := 0; i < enqueueLength; i++ {
		pqueue.enqueue() <- Envelope{
			ChannelID: 0x01,
			Message:   &testMessage{Value: "foo"}, // 5 bytes
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pqueue.process(ctx)

	// sleep to allow context switch for process() to run
	time.Sleep(10 * time.Millisecond)
	doneCh := make(chan struct{})
	go func() {
		pqueue.close()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("pqueue failed to close")
	}
}
