package abciclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
	"github.com/dashpay/tenderdash/libs/service"
)

// A gRPC client.
type grpcClient struct {
	service.BaseService
	logger log.Logger

	mustConnect bool

	client types.ABCIApplicationClient
	conn   *grpc.ClientConn

	mtx  sync.Mutex
	addr string
	err  error

	// map between method name (in grpc format, for example `/tendermint.abci.ABCIApplication/Echo`)
	// and a channel that will be used to limit the number of concurrent requests for that method.
	//
	// If the value is nil, no limit is enforced.
	//
	// Not thread-safe, only modify this before starting the client.
	concurrency map[string]chan struct{}
}

var _ Client = (*grpcClient)(nil)

// NewGRPCClient creates a gRPC client, which will connect to addr upon the
// start. Note Client#Start returns an error if connection is unsuccessful and
// mustConnect is true.
func NewGRPCClient(logger log.Logger, addr string, concurrency map[string]uint16, mustConnect bool) Client {
	cli := &grpcClient{
		logger:      logger,
		addr:        addr,
		mustConnect: mustConnect,
		concurrency: make(map[string]chan struct{}, 20),
	}
	cli.BaseService = *service.NewBaseService(logger, "grpcClient", cli)
	cli.SetMaxConcurrentStreams(concurrency)

	return cli
}

func methodID(method string) string {
	if pos := strings.LastIndex(method, "/"); pos > 0 {
		method = method[pos+1:]
	}

	return strings.ToLower(method)
}

// SetMaxConcurrentStreams sets the maximum number of concurrent streams to be
// allowed on this client.
//
// Not thread-safe, only use this before starting the client.
//
// If limit is 0, no limit is enforced.
func (cli *grpcClient) SetMaxConcurrentStreamsForMethod(method string, n uint16) {
	if cli.IsRunning() {
		panic("cannot set max concurrent streams after starting the client")
	}
	var ch chan struct{}
	if n != 0 {
		ch = make(chan struct{}, n)
	}

	cli.mtx.Lock()
	cli.concurrency[methodID(method)] = ch
	cli.mtx.Unlock()
}

// SetMaxConcurrentStreams sets the maximum number of concurrent streams to be
// allowed on this client.
// # Arguments
//
// * `methods` - A map between method name (in grpc format, for example `/tendermint.abci.ABCIApplication/Echo`)
// and the maximum number of concurrent streams to be allowed for that method.
//
// Special method name "*" can be used to set the default limit for methods not explicitly listed.
//
// If the value is 0, no limit is enforced.
//
// Not thread-safe, only use this before starting the client.
func (cli *grpcClient) SetMaxConcurrentStreams(methods map[string]uint16) {
	for method, n := range methods {
		cli.SetMaxConcurrentStreamsForMethod(method, n)
	}
}

func dialerFunc(_ctx context.Context, addr string) (net.Conn, error) {
	return tmnet.Connect(addr)
}

// rateLimit blocks until the client is allowed to send a request.
// It returns a function that should be called after the request is done.
//
// method should be the method name in grpc format, for example `/tendermint.abci.ABCIApplication/Echo`.
// Special method name "*" can be used to define the default limit.
// If no limit is set for the method, the default limit is used.
func (cli *grpcClient) rateLimit(method string) context.CancelFunc {
	ch := cli.concurrency[methodID(method)]
	// handle default
	if ch == nil {
		ch = cli.concurrency["*"]
	}
	if ch == nil {
		return func() {}
	}

	cli.logger.Trace("grpcClient rateLimit", "addr", cli.addr)
	ch <- struct{}{}
	return func() { <-ch }
}

func (cli *grpcClient) unaryClientInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	done := cli.rateLimit(method)
	defer done()

	return invoker(ctx, method, req, reply, cc, opts...)
}

func (cli *grpcClient) streamClientInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	done := cli.rateLimit(method)
	defer done()

	return streamer(ctx, desc, cc, method, opts...)
}

func (cli *grpcClient) OnStart(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()

RETRY_LOOP:
	for {
		conn, err := grpc.NewClient(cli.addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(dialerFunc),
			grpc.WithChainUnaryInterceptor(cli.unaryClientInterceptor),
			grpc.WithChainStreamInterceptor(cli.streamClientInterceptor),
		)
		if err != nil {
			if cli.mustConnect {
				return err
			}
			cli.logger.Error("abci.grpcClient failed to connect,  Retrying...", "addr", cli.addr, "err", err)
			timer.Reset(time.Second * dialRetryIntervalSeconds)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				continue RETRY_LOOP
			}
		}

		cli.logger.Info("Dialed server. Waiting for echo.", "addr", cli.addr)
		client := types.NewABCIApplicationClient(conn)
		cli.conn = conn

	ENSURE_CONNECTED:
		for {
			_, err := client.Echo(ctx, &types.RequestEcho{Message: "hello"}, grpc.WaitForReady(true))
			if err == nil {
				break ENSURE_CONNECTED
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}

			cli.logger.Error("Echo failed", "err", err)
			timer.Reset(time.Second * echoRetryIntervalSeconds)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				continue ENSURE_CONNECTED
			}
		}

		cli.client = client
		return nil
	}
}

func (cli *grpcClient) OnStop() {
	cli.mtx.Lock()
	defer cli.mtx.Unlock()

	if cli.conn != nil {
		cli.err = cli.conn.Close()
	}
}

func (cli *grpcClient) Error() error {
	cli.mtx.Lock()
	defer cli.mtx.Unlock()

	return cli.err
}

//----------------------------------------

func (cli *grpcClient) Flush(_ctx context.Context) error { return nil }

func (cli *grpcClient) Echo(ctx context.Context, msg string) (*types.ResponseEcho, error) {
	return cli.client.Echo(ctx, types.ToRequestEcho(msg).GetEcho(), grpc.WaitForReady(true))
}

func (cli *grpcClient) Info(ctx context.Context, params *types.RequestInfo) (*types.ResponseInfo, error) {
	return cli.client.Info(ctx, types.ToRequestInfo(params).GetInfo(), grpc.WaitForReady(true))
}

func (cli *grpcClient) CheckTx(ctx context.Context, params *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	return cli.client.CheckTx(ctx, types.ToRequestCheckTx(params).GetCheckTx(), grpc.WaitForReady(true))
}

func (cli *grpcClient) Query(ctx context.Context, params *types.RequestQuery) (*types.ResponseQuery, error) {
	return cli.client.Query(ctx, types.ToRequestQuery(params).GetQuery(), grpc.WaitForReady(true))
}

func (cli *grpcClient) InitChain(ctx context.Context, params *types.RequestInitChain) (*types.ResponseInitChain, error) {
	return cli.client.InitChain(ctx, types.ToRequestInitChain(params).GetInitChain(), grpc.WaitForReady(true))
}

func (cli *grpcClient) ListSnapshots(ctx context.Context, params *types.RequestListSnapshots) (*types.ResponseListSnapshots, error) {
	return cli.client.ListSnapshots(ctx, types.ToRequestListSnapshots(params).GetListSnapshots(), grpc.WaitForReady(true))
}

func (cli *grpcClient) OfferSnapshot(ctx context.Context, params *types.RequestOfferSnapshot) (*types.ResponseOfferSnapshot, error) {
	return cli.client.OfferSnapshot(ctx, types.ToRequestOfferSnapshot(params).GetOfferSnapshot(), grpc.WaitForReady(true))
}

func (cli *grpcClient) LoadSnapshotChunk(ctx context.Context, params *types.RequestLoadSnapshotChunk) (*types.ResponseLoadSnapshotChunk, error) {
	return cli.client.LoadSnapshotChunk(ctx, types.ToRequestLoadSnapshotChunk(params).GetLoadSnapshotChunk(), grpc.WaitForReady(true))
}

func (cli *grpcClient) ApplySnapshotChunk(ctx context.Context, params *types.RequestApplySnapshotChunk) (*types.ResponseApplySnapshotChunk, error) {
	return cli.client.ApplySnapshotChunk(ctx, types.ToRequestApplySnapshotChunk(params).GetApplySnapshotChunk(), grpc.WaitForReady(true))
}

func (cli *grpcClient) PrepareProposal(ctx context.Context, params *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	return cli.client.PrepareProposal(ctx, types.ToRequestPrepareProposal(params).GetPrepareProposal(), grpc.WaitForReady(true))
}

func (cli *grpcClient) ProcessProposal(ctx context.Context, params *types.RequestProcessProposal) (*types.ResponseProcessProposal, error) {
	return cli.client.ProcessProposal(ctx, types.ToRequestProcessProposal(params).GetProcessProposal(), grpc.WaitForReady(true))
}

func (cli *grpcClient) ExtendVote(ctx context.Context, params *types.RequestExtendVote) (*types.ResponseExtendVote, error) {
	return cli.client.ExtendVote(ctx, types.ToRequestExtendVote(params).GetExtendVote(), grpc.WaitForReady(true))
}

func (cli *grpcClient) VerifyVoteExtension(ctx context.Context, params *types.RequestVerifyVoteExtension) (*types.ResponseVerifyVoteExtension, error) {
	return cli.client.VerifyVoteExtension(ctx, types.ToRequestVerifyVoteExtension(params).GetVerifyVoteExtension(), grpc.WaitForReady(true))
}

func (cli *grpcClient) FinalizeBlock(ctx context.Context, params *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	return cli.client.FinalizeBlock(ctx, types.ToRequestFinalizeBlock(params).GetFinalizeBlock(), grpc.WaitForReady(true))
}
