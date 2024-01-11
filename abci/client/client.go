package abciclient

import (
	"context"
	"fmt"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

const (
	dialRetryIntervalSeconds = 3
	echoRetryIntervalSeconds = 1
)

//go:generate ../../scripts/mockery_generate.sh Client

// Client defines the interface for an ABCI client.
//
// NOTE these are client errors, eg. ABCI socket connectivity issues.
// Application-related errors are reflected in response via ABCI error codes
// and (potentially) error response.
type Client interface {
	service.Service
	types.Application

	Error() error
	Flush(context.Context) error
	Echo(context.Context, string) (*types.ResponseEcho, error)
}

//----------------------------------------

// NewClient returns a new ABCI client of the specified transport type.
// It returns an error if the transport is not "socket" or "grpc"
func NewClient(logger log.Logger, addr, transport string, mustConnect bool, metrics *Metrics) (Client, error) {
	switch transport {
	case "socket":
		return NewSocketClient(logger, addr, mustConnect, metrics), nil
	case "grpc":
		return NewGRPCClient(logger, addr, mustConnect), nil
	default:
		return nil, fmt.Errorf("unknown abci transport %s", transport)
	}
}

type requestAndResponse struct {
	*types.Request
	*types.Response

	mtx    sync.Mutex
	signal chan struct{}
}

// priority of this request type; higher number means more important
func (reqResp *requestAndResponse) priority() (priority int8) {
	if reqResp == nil || reqResp.Request == nil || reqResp.Request.Value == nil {
		// Error
		return -128
	}
	// consensus-related requests are more important
	switch reqResp.Request.Value.(type) {
	case *types.Request_InitChain:
		priority = 110
	case *types.Request_PrepareProposal:
		priority = 100
	case *types.Request_ProcessProposal:
		priority = 100
	case *types.Request_FinalizeBlock:
		priority = 100
	case *types.Request_Flush:
		priority = 100
	case *types.Request_ExtendVote:
		priority = 100
	case *types.Request_VerifyVoteExtension:
		priority = 90
	case *types.Request_Info:
		priority = 120
	case *types.Request_CheckTx:
		priority = -10
	case *types.Request_Query:
		priority = -20
	default:
		priority = 0
	}

	return priority
}

func makeReqRes(req *types.Request) *requestAndResponse {
	return &requestAndResponse{
		Request:  req,
		Response: nil,
		signal:   make(chan struct{}),
	}
}

// markDone marks the ReqRes object as done.
func (r *requestAndResponse) markDone() {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	close(r.signal)
}
