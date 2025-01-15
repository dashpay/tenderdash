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
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

type routedClient struct {
	service.Service
	logger        log.Logger
	routing       Routing
	defaultClient ClientInfo
}

var _ Client = (*routedClient)(nil)

type RequestType string
type Routing map[RequestType][]ClientInfo

type ClientInfo struct {
	Client
	// ClientID is an unique, human-readable, client identifier
	ClientID string
}

// NewRoutedClientWithAddr returns a new ABCI client that routes requests to multiple
// underlying clients based on the request type.
//
// It takes a comma-separated list of routing rules, consisting of colon-separated: request type, transport, address.
// Special request type "*" is used for default client.
//
// Example:
//
// ```
//
//	"Info:socket:unix:///tmp/socket.1,Info:socket:unix:///tmp/socket.2,CheckTx:socket:unix:///tmp/socket.1,*:socket:unix:///tmp/socket.3"
//
// ```
//
// # Arguments
//   - `logger` - The logger to use for the client.
//   - `addr` - comma-separated list of routing rules, consisting of request type, transport name and client address separated with colon.
//     Special request type "*" is used for default client.
func NewRoutedClientWithAddr(logger log.Logger, addr string, mustConnect bool) (Client, error) {
	// Split the routing rules
	routing := make(Routing)
	clients := make(map[string]Client)
	var defaultClient Client

	rules := strings.Split(addr, ",")

	for _, rule := range rules {
		parts := strings.SplitN(rule, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid routing rule: %s", rule)
		}
		requestType := strings.TrimSpace(parts[0])
		transport := strings.TrimSpace(parts[1])
		address := strings.TrimSpace(parts[2])

		// Create a new client if it doesn't exist
		clientName := fmt.Sprintf("%s:%s", transport, address)
		if _, ok := clients[clientName]; !ok {
			cfg := config.AbciConfig{Address: address, Transport: transport}
			c, err := NewClient(logger, cfg, mustConnect)
			if err != nil {
				return nil, err
			}
			clients[clientName] = c
		}

		// Add the client to the routing table
		if requestType == "*" {
			if defaultClient != nil {
				return nil, fmt.Errorf("multiple default clients")
			}
			defaultClient = clients[clientName]
			continue
		}

		client := clients[clientName]
		routing[RequestType(requestType)] = append(routing[RequestType(requestType)], ClientInfo{client, clientName})
	}

	if defaultClient == nil {
		return nil, fmt.Errorf("no default client defined for routed client address %s", addr)
	}

	return NewRoutedClient(logger, defaultClient, routing)
}

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
func NewRoutedClient(logger log.Logger, defaultClient Client, routing Routing) (Client, error) {
	defaultClientID := ""
	if s, ok := defaultClient.(fmt.Stringer); ok {
		defaultClientID = fmt.Sprintf("DEFAULT:%s", s.String())
	} else {
		defaultClientID = "DEFAULT"
	}

	cli := &routedClient{
		logger:        logger,
		routing:       routing,
		defaultClient: ClientInfo{defaultClient, defaultClientID},
	}

	cli.Service = service.NewBaseService(logger, "RoutedClient", cli)
	return cli, nil
}

func (cli *routedClient) OnStart(ctx context.Context) error {
	var errs error
	for _, clients := range cli.routing {
		for _, client := range clients {
			if !client.IsRunning() {
				if err := client.Start(ctx); err != nil {
					errs = multierror.Append(errs, err)
				}
			}
		}
	}

	if !cli.defaultClient.IsRunning() {
		if err := cli.defaultClient.Start(ctx); err != nil {
			errs = multierror.Append(errs, err)
		}
	}

	return errs
}

func (cli *routedClient) OnStop() {
	for _, clients := range cli.routing {
		for _, client := range clients {
			if client.IsRunning() {
				switch c := client.Client.(type) {
				case *socketClient:
					c.Stop()
				case *localClient:
					c.Stop()
				case *grpcClient:
					c.Stop()
				}
			}
		}
	}
}

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
// If all clients return only zero values, it returns response from last client, which is effectively
// also a zero value.
//
// If the function returns only 1 value, it assumes it is of type `error`.
// Otherwise it assumes response is `result, error`.
//
// When a function call returns an error, error is returned and remaining functions are NOT called.
func (cli *routedClient) delegate(args ...interface{}) (firstResult any, err error) {
	// Get the caller function name; it will be our request type
	pc, _, _, _ := runtime.Caller(1)
	funcObj := runtime.FuncForPC(pc)
	funcName := funcObj.Name()
	// remove package name
	funcName = funcName[strings.LastIndex(funcName, ".")+1:]

	clients, ok := cli.routing[RequestType(funcName)]
	if !ok {
		clients = []ClientInfo{cli.defaultClient}
		cli.logger.Trace("no client found for method, falling back to default client", "method", funcName)
	}
	// client that provided first non-zero result
	winner := ""

	startAll := time.Now()

	var ret any
	for _, client := range clients {
		start := time.Now()

		zerosReturned, results := cli.call(client, funcName, args...)
		if ret, err = parseReturned(funcName, results); err != nil {
			cli.logger.Error("abci client returned error", "client_id", client.ClientID, "err", err)
			return ret, err
		}

		// return first non-zero result
		if !zerosReturned && firstResult == nil {
			firstResult = ret
			winner = client.ClientID
		}

		cli.logger.Trace("routed ABCI request to a client",
			"method", funcName,
			"client_id", client.ClientID,
			"nil", zerosReturned,
			"took", time.Since(start).String())
	}

	cli.logger.Trace("routed ABCI request execution successful",
		"method", funcName,
		"client_id", winner,
		"took", time.Since(startAll).String(),
		"nil", firstResult == nil)

	if firstResult == nil {
		firstResult = ret
	}

	return firstResult, err
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

func parseReturned(funcName string, ret []interface{}) (any, error) {
	switch len(ret) {
	case 0:
		// should never happen
		return nil, fmt.Errorf("no result from any client for ABCI method %s", funcName)
	case 1:
		err, _ := ret[0].(error)
		return nil, err

	case 2:
		err, _ := ret[1].(error)
		return ret[0], err
	default:
		panic(fmt.Sprintf("unexpected number of return values: %d", len(ret)))
	}
}

// Error returns an error if the client was stopped abruptly.
func (cli *routedClient) Error() error {
	var errs error
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

func (cli *routedClient) FinalizeSnapshot(ctx context.Context, req *types.RequestFinalizeSnapshot) (*types.ResponseFinalizeSnapshot, error) {
	result, err := cli.delegate(ctx, req)
	return result.(*types.ResponseFinalizeSnapshot), err
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
