package abciclient

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

type routedClient struct {
	service.Service
	logger        log.Logger
	routing       Routing
	defaultClient Client
}

var _ Client = (*routedClient)(nil)

type RequestType string
type Routing map[RequestType][]Client

// NewRoutedClient returns a new ABCI client that routes requests to the
// appropriate underlying client based on the request type.
//
// # Arguments
//
//   - `logger` - The logger to use for the client.
//   - `defaultClient` - The default client to use when no specific client is
//     configured for a request type.
//   - `routing` - The clients to route requests to.
//
// See docs of routedClient.delegate() for more details about implemented logic.
//
// TODO: Implement metrics for routed client and separate metrics for underlying clients.
func NewRoutedClient(
	logger log.Logger, defaultClient Client, routing Routing) (Client, error) {

	cli := &routedClient{
		logger:        logger,
		routing:       routing,
		defaultClient: defaultClient,
	}

	cli.Service = service.NewBaseService(logger, "RoutedClient", cli)
	return cli, nil
}

func (cli *routedClient) OnStart(context.Context) error {
	return nil
}

func (cli *routedClient) OnStop() {
}

// // getClient returns the client for the caller function
// func (cli *routedClient) getClient() (Client, error) {
// 	// Get the caller function name; it will be our request type
// 	pc, _, _, _ := runtime.Caller(1)
// 	funcObj := runtime.FuncForPC(pc)
// 	funcName := funcObj.Name()
// 	// remove package name
// 	funcName = funcName[strings.LastIndex(funcName, ".")+1:]

// 	client, ok := cli.routing[RequestType(funcName)]
// 	if !ok {
// 		if cli.defaultClient != nil {
// 			return *cli.defaultClient, nil
// 		}

// 		return nil, fmt.Errorf("no client for ABCI method %s", funcName)
// 	}

// 	return client, nil
// }

// delegate calls the given function on the appropriate client with the given
// arguments.
//
// It executes the given function on all clients configured in the routing table.
// If no client is configured for the given function, it calls the function on the
// default client.
//
// If more than one client is configured for the given function, it calls the
// function on each client in turn, and returns first result where any of returned
// values is non-zero. Results of subsequent calls are silently dropped.
//
// If all clients return only zero values, it returns response from last client.
//
// If the function returns only 1 value, it assumes it is of type `error`.
// Otherwise it assumes response is `result, error`.
func (cli *routedClient) delegate(args ...interface{}) (res any, err error) {
	// Get the caller function name; it will be our request type
	pc, _, _, _ := runtime.Caller(1)
	funcObj := runtime.FuncForPC(pc)
	funcName := funcObj.Name()
	// remove package name
	funcName = funcName[strings.LastIndex(funcName, ".")+1:]

	clients, ok := cli.routing[RequestType(funcName)]
	if !ok {
		clients = []Client{cli.defaultClient}
	}

	var firstResult []interface{}
	winner := ""

	startAll := time.Now()

	var results []interface{}
	for _, client := range clients {
		zerosReturned := false
		start := time.Now()

		zerosReturned, results = cli.call(client, funcName, args...)
		// return first non-zero result
		if !zerosReturned && firstResult == nil {
			firstResult = results
			winner = reflect.TypeOf(client).String()
		}

		// TODO change to Trace
		cli.logger.Debug("routed ABCI request to a client",
			"method", funcName,
			"client", reflect.TypeOf(client).String(),
			"nil", zerosReturned,
			"took", time.Since(start).String())
	}

	// TODO change to Trace
	cli.logger.Debug("routed ABCI request execution finished",
		"method", funcName,
		"winner", winner,
		"took", time.Since(startAll).String(),
		"nil", firstResult == nil)

	if firstResult == nil {
		firstResult = results
	}

	switch len(firstResult) {
	case 0:
		// should never happen
		return nil, fmt.Errorf("no result from any client for ABCI method %s", funcName)
	case 1:
		err, _ := firstResult[0].(error)
		return nil, err

	case 2:
		err, _ := firstResult[1].(error)
		return firstResult[0], err
	default:
		panic(fmt.Sprintf("unexpected number of return values: %d", len(firstResult)))
	}
}

// call calls the given function on the given client with the given arguments.
// It returns whether all returned values are zero, and these values itself.
func (cli *routedClient) call(client Client, funcName string, args ...interface{}) (onlyZeros bool, result []interface{}) {
	method := reflect.ValueOf(client).MethodByName(funcName)
	if !method.IsValid() {
		panic(fmt.Sprintf("no method %s on client %T", funcName, client))
	}

	arguments := make([]reflect.Value, 0, len(args))
	for _, arg := range args {
		arguments = append(arguments, reflect.ValueOf(arg))
	}

	values := method.Call(arguments)

	onlyZeros = true

	result = make([]interface{}, 0, len(values))
	for _, v := range values {
		if !v.IsZero() {
			onlyZeros = false
		}
		result = append(result, v.Interface())
	}

	return onlyZeros, result
}

// Error returns an error if the client was stopped abruptly.
func (cli *routedClient) Error() error {
	var errs *multierror.Error
	for _, clients := range cli.routing {
		for _, client := range clients {
			err := client.Error()
			if err != nil {
				errs = multierror.Append(errs, err)
			}
		}
	}

	return errs
}

/// Implement the Application interface

func (cli *routedClient) Flush(ctx context.Context) error {
	_, err := cli.delegate(ctx)
	return err
}

func (cli *routedClient) Echo(ctx context.Context, msg string) (*types.ResponseEcho, error) {
	result, err := cli.delegate(ctx, msg)
	return result.(*types.ResponseEcho), err
}

func (cli *routedClient) Info(ctx context.Context, req *types.RequestInfo) (*types.ResponseInfo, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseInfo), err
}

func (cli *routedClient) CheckTx(ctx context.Context, req *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseCheckTx), err
}

func (cli *routedClient) Query(ctx context.Context, req *types.RequestQuery) (*types.ResponseQuery, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseQuery), err
}

func (cli *routedClient) InitChain(ctx context.Context, req *types.RequestInitChain) (*types.ResponseInitChain, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseInitChain), err
}

func (cli *routedClient) ListSnapshots(ctx context.Context, req *types.RequestListSnapshots) (*types.ResponseListSnapshots, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseListSnapshots), err
}

func (cli *routedClient) OfferSnapshot(ctx context.Context, req *types.RequestOfferSnapshot) (*types.ResponseOfferSnapshot, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseOfferSnapshot), err
}

func (cli *routedClient) LoadSnapshotChunk(ctx context.Context, req *types.RequestLoadSnapshotChunk) (*types.ResponseLoadSnapshotChunk, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseLoadSnapshotChunk), err
}

func (cli *routedClient) ApplySnapshotChunk(ctx context.Context, req *types.RequestApplySnapshotChunk) (*types.ResponseApplySnapshotChunk, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseApplySnapshotChunk), err
}

func (cli *routedClient) PrepareProposal(ctx context.Context, req *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponsePrepareProposal), err
}

func (cli *routedClient) ProcessProposal(ctx context.Context, req *types.RequestProcessProposal) (*types.ResponseProcessProposal, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseProcessProposal), err
}

func (cli *routedClient) ExtendVote(ctx context.Context, req *types.RequestExtendVote) (*types.ResponseExtendVote, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseExtendVote), err
}

func (cli *routedClient) VerifyVoteExtension(ctx context.Context, req *types.RequestVerifyVoteExtension) (*types.ResponseVerifyVoteExtension, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseVerifyVoteExtension), err
}

func (cli *routedClient) FinalizeBlock(ctx context.Context, req *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseFinalizeBlock), err
}
