package abciclient

import (
	"context"
	"fmt"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
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
func NewClient(logger log.Logger, cfg config.AbciConfig, mustConnect bool) (Client, error) {
	transport := cfg.Transport
	addr := cfg.Address

	switch transport {
	case "socket":
		return NewSocketClient(logger, addr, mustConnect), nil
	case "grpc":
		return NewGRPCClient(logger, addr, cfg.GrpcConcurrency, mustConnect), nil
	case "routed":
		return NewRoutedClientWithAddr(logger, addr, mustConnect)
	default:
		return nil, fmt.Errorf("unknown abci transport %s", transport)
	}
}

type requestAndResponse struct {
	*types.Request
	*types.Response

	mtx sync.Mutex
	// context for the request; we check if it's not expired before sending
	ctx    context.Context
	signal chan struct{}
}

func makeReqRes(ctx context.Context, req *types.Request) *requestAndResponse {
	return &requestAndResponse{
		Request:  req,
		Response: nil,
		ctx:      ctx,
		signal:   make(chan struct{}),
	}
}

// markDone marks the ReqRes object as done.
func (reqResp *requestAndResponse) markDone() {
	reqResp.mtx.Lock()
	defer reqResp.mtx.Unlock()

	close(reqResp.signal)
}
