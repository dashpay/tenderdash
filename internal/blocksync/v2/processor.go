package v2

import (
	"fmt"

	tmstate "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/types"
)

// Events generated by the processor:
// block execution failure, event will indicate the peer(s) that caused the error
type pcBlockVerificationFailure struct {
	priorityNormal
	height       int64
	firstPeerID  types.NodeID
	secondPeerID types.NodeID
}

func (e pcBlockVerificationFailure) String() string {
	return fmt.Sprintf("pcBlockVerificationFailure{%d 1st peer: %v, 2nd peer: %v}",
		e.height, e.firstPeerID, e.secondPeerID)
}

// successful block execution
type pcBlockProcessed struct {
	priorityNormal
	height int64
	peerID types.NodeID
}

func (e pcBlockProcessed) String() string {
	return fmt.Sprintf("pcBlockProcessed{%d peer: %v}", e.height, e.peerID)
}

// processor has finished
type pcFinished struct {
	priorityNormal
	blocksSynced int
	tmState      tmstate.State
}

func (p pcFinished) Error() string {
	return "finished"
}

type queueItem struct {
	block  *types.Block
	peerID types.NodeID
}

type blockQueue map[int64]queueItem

type pcState struct {
	// blocks waiting to be processed
	queue blockQueue

	// draining indicates that the next rProcessBlock event with a queue miss constitutes completion
	draining bool

	// the number of blocks successfully synced by the processor
	blocksSynced int

	// the processorContext which contains the processor dependencies
	context processorContext
}

func (state *pcState) String() string {
	return fmt.Sprintf("height: %d queue length: %d draining: %v blocks synced: %d",
		state.height(), len(state.queue), state.draining, state.blocksSynced)
}

// newPcState returns a pcState initialized with the last verified block enqueued
func newPcState(context processorContext) *pcState {
	return &pcState{
		queue:        blockQueue{},
		draining:     false,
		blocksSynced: 0,
		context:      context,
	}
}

// nextTwo returns the next two unverified blocks
func (state *pcState) nextTwo() (queueItem, queueItem, error) {
	if first, ok := state.queue[state.height()+1]; ok {
		if second, ok := state.queue[state.height()+2]; ok {
			return first, second, nil
		}
	}
	return queueItem{}, queueItem{}, fmt.Errorf("not found")
}

// synced returns true when at most the last verified block remains in the queue
func (state *pcState) synced() bool {
	return len(state.queue) <= 1
}

func (state *pcState) enqueue(peerID types.NodeID, block *types.Block, height int64) {
	if item, ok := state.queue[height]; ok {
		panic(fmt.Sprintf(
			"duplicate block %d (%X) enqueued by processor (sent by %v; existing block %X from %v)",
			height, block.Hash(), peerID, item.block.Hash(), item.peerID))
	}

	state.queue[height] = queueItem{block: block, peerID: peerID}
}

func (state *pcState) height() int64 {
	return state.context.tmState().LastBlockHeight
}

// purgePeer moves all unprocessed blocks from the queue
func (state *pcState) purgePeer(peerID types.NodeID) {
	// what if height is less than state.height?
	for height, item := range state.queue {
		if item.peerID == peerID {
			delete(state.queue, height)
		}
	}
}

// handle processes FSM events
func (state *pcState) handle(event Event) (Event, error) {
	switch event := event.(type) {
	case bcResetState:
		state.context.setState(event.state)
		return noOp, nil

	case scFinishedEv:
		if state.synced() {
			return pcFinished{tmState: state.context.tmState(), blocksSynced: state.blocksSynced}, nil
		}
		state.draining = true
		return noOp, nil

	case scPeerError:
		state.purgePeer(event.peerID)
		return noOp, nil

	case scBlockReceived:
		if event.block == nil {
			return noOp, nil
		}

		// enqueue block if height is higher than state height, else ignore it
		if event.block.Height > state.height() {
			state.enqueue(event.peerID, event.block, event.block.Height)
		}
		return noOp, nil

	case rProcessBlock:
		tmstate := state.context.tmState()
		firstItem, secondItem, err := state.nextTwo()
		if err != nil {
			if state.draining {
				return pcFinished{tmState: tmstate, blocksSynced: state.blocksSynced}, nil
			}
			return noOp, nil
		}

		var (
			first, second = firstItem.block, secondItem.block
			firstParts    = first.MakePartSet(types.BlockPartSizeBytes)
			firstID       = types.BlockID{Hash: first.Hash(), PartSetHeader: firstParts.Header()}
			firstStateID  = types.StateID{Height: first.Height - 1, LastAppHash: first.AppHash}
		)

		// verify if +second+ last commit "confirms" +first+ block
		err = state.context.verifyCommit(tmstate.ChainID, firstID, firstStateID, first.Height, second.LastCommit)
		if err != nil {
			state.purgePeer(firstItem.peerID)
			if firstItem.peerID != secondItem.peerID {
				state.purgePeer(secondItem.peerID)
			}
			return pcBlockVerificationFailure{
					height: first.Height, firstPeerID: firstItem.peerID, secondPeerID: secondItem.peerID},
				nil
		}

		state.context.saveBlock(first, firstParts, second.LastCommit)

		if err := state.context.applyBlock(firstID, first); err != nil {
			panic(fmt.Sprintf("failed to process committed block (%d:%X): %v", first.Height, first.Hash(), err))
		}

		state.context.recordConsMetrics(first)

		delete(state.queue, first.Height)
		state.blocksSynced++

		return pcBlockProcessed{height: first.Height, peerID: firstItem.peerID}, nil
	}

	return noOp, nil
}