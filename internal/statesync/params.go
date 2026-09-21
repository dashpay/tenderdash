package statesync

import (
	"context"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/p2p"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
)

type paramsRequest struct {
	ctx       context.Context
	cancel    context.CancelFunc
	peer      types.NodeID
	height    uint64
	connID    uint64
	response  chan types.ConsensusParams
	delivered bool
}

// paramsRequests binds pending requests to the connection that was current at send time.
type paramsRequests struct {
	mtx         sync.Mutex
	connections map[types.NodeID]uint64
	pending     map[*paramsRequest]struct{}
}

func (p *paramsRequests) update(update p2p.PeerUpdate) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.connections == nil {
		p.connections = make(map[types.NodeID]uint64)
	}
	for request := range p.pending {
		if request.peer == update.NodeID {
			delete(p.pending, request)
			request.cancel()
		}
	}
	if update.Status == p2p.PeerStatusUp {
		p.connections[update.NodeID] = update.ConnID
	} else {
		delete(p.connections, update.NodeID)
	}
}

func (p *paramsRequests) register(ctx context.Context, peer types.NodeID, height uint64) *paramsRequest {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	connID, ok := p.connections[peer]
	if !ok || ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	request := &paramsRequest{ctx: ctx, cancel: cancel, peer: peer, height: height, connID: connID,
		response: make(chan types.ConsensusParams, 1)}
	if p.pending == nil {
		p.pending = make(map[*paramsRequest]struct{})
	}
	p.pending[request] = struct{}{}
	return request
}

func (p *paramsRequests) remove(request *paramsRequest) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	delete(p.pending, request)
	if request != nil {
		request.cancel()
	}
}

func (p *paramsRequests) respond(envelope *p2p.Envelope, response *ssproto.ParamsResponse) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	for request := range p.pending {
		if request.peer != envelope.From || request.height != response.Height || request.connID != envelope.ConnID {
			continue
		}
		if request.ctx.Err() != nil {
			delete(p.pending, request)
		} else if !request.delivered {
			request.delivered = true
			// The single buffered delivery never waits for the request worker.
			request.response <- types.ConsensusParamsFromProto(response.ConsensusParams)
		}
	}
}
