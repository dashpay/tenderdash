package server

import (
	"context"
	"net"

	"google.golang.org/grpc"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
	"github.com/dashpay/tenderdash/libs/service"
)

type GRPCServer struct {
	service.BaseService
	logger log.Logger

	proto  string
	addr   string
	server *grpc.Server

	app types.Application
}

// NewGRPCServer returns a new gRPC ABCI server
func NewGRPCServer(logger log.Logger, protoAddr string, app types.Application) service.Service {
	proto, addr := tmnet.ProtocolAndAddress(protoAddr)
	s := &GRPCServer{
		logger: logger,
		proto:  proto,
		addr:   addr,
		app:    app,
	}
	s.BaseService = *service.NewBaseService(logger, "ABCIServer", s)
	return s
}

// OnStart starts the gRPC service.
func (s *GRPCServer) OnStart(ctx context.Context) error {
	ln, err := net.Listen(s.proto, s.addr)
	if err != nil {
		return err
	}

	s.server = grpc.NewServer()
	types.RegisterABCIApplicationServer(s.server, &gRPCApplication{Application: s.app})

	s.logger.Info("Listening", "proto", s.proto, "addr", s.addr)
	go func() {
		go func() {
			<-ctx.Done()
			s.server.GracefulStop()
		}()

		if err := s.server.Serve(ln); err != nil {
			s.logger.Error("error serving gRPC server", "err", err)
		}
	}()
	return nil
}

// OnStop stops the gRPC server.
func (s *GRPCServer) OnStop() { s.server.Stop() }

//-------------------------------------------------------

// gRPCApplication is a gRPC shim for Application
type gRPCApplication struct {
	types.Application
}

func (app *gRPCApplication) Echo(_ context.Context, req *types.RequestEcho) (*types.ResponseEcho, error) {
	return &types.ResponseEcho{Message: req.Message}, nil
}

func (app *gRPCApplication) Flush(_ context.Context, _req *types.RequestFlush) (*types.ResponseFlush, error) {
	return &types.ResponseFlush{}, nil
}
