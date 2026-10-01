package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/dash"
)

type blockedFinalizeApp struct {
	abci.Application
	entered chan context.Context
	release chan struct{}
}

func (a *blockedFinalizeApp) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	a.entered <- ctx
	<-a.release
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.Application.FinalizeBlock(ctx, req)
}

func TestStateStopDuringFinalize(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		name := "manual stop drains commit"
		if cancelParent {
			name = "parent cancellation aborts commit"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			app, err := kvstore.NewMemoryApp()
			require.NoError(t, err)
			defer func() { require.NoError(t, app.Close()) }()
			blocked := &blockedFinalizeApp{Application: app, entered: make(chan context.Context, 1), release: make(chan struct{})}
			cs, _ := makeState(ctx, t, makeStateArgs{validators: 1, application: blocked})
			height := cs.GetCurrentHeight()
			defer func() {
				select {
				case <-blocked.release:
				default:
					close(blocked.release)
				}
				cs.Stop()
				cs.Wait()
			}()
			require.NoError(t, cs.Start(dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)))
			var finalizeCtx context.Context
			select {
			case finalizeCtx = <-blocked.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("consensus did not enter FinalizeBlock")
			}
			require.NotNil(t, finalizeCtx.Value(msgInfoCtx), "processing must retain message metadata")
			cs.Stop()
			waited := make(chan struct{})
			go func() { cs.Wait(); close(waited) }()
			select {
			case <-waited:
				t.Fatal("Wait returned with FinalizeBlock still active")
			case <-time.After(20 * time.Millisecond):
			}
			if cancelParent {
				cancel()
				require.ErrorIs(t, finalizeCtx.Err(), context.Canceled)
			} else {
				require.NoError(t, finalizeCtx.Err(), "manual Stop must preserve an admitted commit")
			}
			close(blocked.release)
			select {
			case <-waited:
			case <-time.After(5 * time.Second):
				t.Fatal("consensus did not drain")
			}
			state, err := cs.stateStore.Load()
			require.NoError(t, err)
			if !cancelParent {
				require.Equal(t, height, state.LastBlockHeight)
			}
		})
	}
}

type blockedStartTicker struct {
	TimeoutTicker
	entered chan context.Context
	release chan struct{}
}

func (t *blockedStartTicker) Start(ctx context.Context) error {
	t.entered <- ctx
	<-t.release
	return t.TimeoutTicker.Start(ctx)
}

func TestConsensusHandoffCallerCanStopWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _ := makeState(ctx, t, makeStateArgs{})
	ticker := &blockedStartTicker{TimeoutTicker: cs.timeoutTicker, entered: make(chan context.Context, 1), release: make(chan struct{})}
	cs.timeoutTicker = ticker
	cs.roundScheduler.timeoutTicker = ticker
	rts := setup(ctx, t, 1, []*State{cs}, 32)
	for _, r := range rts.reactors {
		callerCtx, cancelCaller := context.WithCancel(ctx)
		defer cancelCaller()
		defer func() {
			select {
			case <-ticker.release:
			default:
				close(ticker.release)
			}
			r.Stop()
			r.Wait()
		}()
		returned := make(chan struct{})
		go func() { r.SwitchToConsensus(callerCtx, cs.GetStateData().state, false, 0); close(returned) }()
		var startupCtx context.Context
		select {
		case startupCtx = <-ticker.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("handoff did not enter startup")
		}
		cancelCaller()
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("caller cancellation did not release handoff wait")
		}
		require.NoError(t, startupCtx.Err(), "caller cancellation must not abandon admitted startup")
		r.Stop()
		waited := make(chan struct{})
		go func() { r.Wait(); close(waited) }()
		select {
		case <-waited:
			t.Fatal("reactor stopped before its admitted handoff finished")
		case <-time.After(20 * time.Millisecond):
		}
		close(ticker.release)
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
			t.Fatal("reactor failed to drain handoff")
		}
		require.False(t, cs.IsRunning())
	}
}
