package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/spf13/viper"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/config"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/internal/p2p"
	rpccore "github.com/dashpay/tenderdash/internal/rpc/core"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light"
	lproxy "github.com/dashpay/tenderdash/light/proxy"
	lrpc "github.com/dashpay/tenderdash/light/rpc"
	dbs "github.com/dashpay/tenderdash/light/store/db"
	"github.com/dashpay/tenderdash/node"
	"github.com/dashpay/tenderdash/privval"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
	"github.com/dashpay/tenderdash/test/e2e/app"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/test/e2e/pkg/mockcoreserver"
)

var (
	tmhome            string
	tmcfg             *config.Config
	dashCoreRPCClient dashcore.Client
)

func init() {
	tmhome = os.Getenv("TMHOME")
	if tmhome == "" {
		panic("TMHOME is missed")
	}
	var err error
	tmcfg, _, err = setupNode()
	if err != nil {
		panic("failed to setup config: " + err.Error())
	}
}

// main is the binary entrypoint.
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if len(os.Args) != 2 {
		fmt.Printf("Usage: %v <configfile>", os.Args[0])
		return
	}
	configFile := ""
	if len(os.Args) == 2 {
		configFile = os.Args[1]
	}

	if err := run(ctx, configFile); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

// run runs the application - basically like main() with error handling.
func run(ctx context.Context, configFile string) error {
	cfg, err := LoadConfig(configFile)
	if err != nil {
		return err
	}

	logger, err := log.NewDefaultLogger(log.LogFormatPlain, log.LogLevelInfo)
	if err != nil {
		return err
	}

	// Start app server.
	err = startAppServer(ctx, cfg, logger)
	if err != nil {
		logger.Error("starting node",
			"protocol", cfg.Protocol,
			"mode", cfg.Mode,
			"err", err)
		return err
	}

	// Apparently there's no way to wait for the server, so we just sleep
	for {
		time.Sleep(1 * time.Hour)
	}
}

func startAppServer(ctx context.Context, cfg *Config, logger log.Logger) error {
	// Start mock Dash Core before the node, which queries its quorum data.
	if cfg.PrivValServer != "" {
		err := startMockCoreSrv(cfg, logger)
		if err != nil {
			return err
		}
	}
	if cfg.Mode == string(e2e.ModeLight) {
		return startLightNode(ctx, logger, cfg)
	}
	switch cfg.Protocol {
	case "socket", "grpc":
		return startApp(ctx, logger, cfg)
	case "builtin":
		if cfg.Mode == string(e2e.ModeSeed) {
			return startSeedNode(ctx)
		}
		return startNode(ctx, cfg)
	}
	return fmt.Errorf("invalid protocol %q", cfg.Protocol)
}

func startMockCoreSrv(cfg *Config, logger log.Logger) error {
	fmt.Printf("Starting mock core server at address %v\n", cfg.PrivValServer)
	// Start mock core-server
	coreSrv, err := setupCoreServer(cfg)
	if err != nil {
		return fmt.Errorf("unable to setup mock core server: %w", err)
	}
	go func() {
		coreSrv.Start()
	}()

	dashCoreRPCClient, err = dashcore.NewRPCClient(
		cfg.PrivValServer,
		tmcfg.PrivValidator.CoreRPCUsername,
		tmcfg.PrivValidator.CoreRPCPassword,
		logger.With("module", dashcore.ModuleName),
	)
	if err != nil {
		return fmt.Errorf("connection to Dash Core RPC failed: %w", err)
	}
	return nil
}

// startApp starts the application server, listening for connections from Tendermint.
func startApp(ctx context.Context, logger log.Logger, cfg *Config) error {
	app, err := app.NewApplication(*cfg.App(), kvstore.WithCommitVerification())
	if err != nil {
		return err
	}
	if cfg.FinalizeHold > 0 {
		// The hold samples node metrics in-process, so it needs the builtin protocol.
		return errors.New("finalize_hold requires the builtin ABCI protocol")
	}
	srv, err := server.NewServer(logger, cfg.Listen, cfg.Protocol, app)
	if err != nil {
		return err
	}
	err = srv.Start(ctx)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Server listening on %v (%v protocol)", cfg.Listen, cfg.Protocol))
	return nil
}

// startNode starts a Tenderdash node running the application directly. It assumes the Tenderdash
// configuration is in $TMHOME/config/tenderdash.toml.
//
// FIXME There is no way to simply load the configuration from a file, so we need to pull in Viper.
func startNode(ctx context.Context, cfg *Config) error {
	app, err := app.NewApplication(*cfg.App(), kvstore.WithCommitVerification())
	if err != nil {
		return err
	}
	if err := app.SetFinalizeHold(cfg.FinalizeHold, cfg.Dir); err != nil {
		return err
	}

	tmcfg, nodeLogger, err := setupNode()
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	n, err := node.New(
		ctx,
		tmcfg,
		nodeLogger,
		abciclient.NewLocalClient(nodeLogger, app),
		nil,
	)
	if err != nil {
		return err
	}
	if cfg.FinalizeHold > 0 {
		env, ok := n.(interface{ RPCEnvironment() *rpccore.Environment })
		if !ok {
			return errors.New("finalize_hold: node does not expose its block store")
		}
		app.SetBlockStoreHeight(env.RPCEnvironment().BlockStore.Height)
	}
	return n.Start(ctx)
}

func startSeedNode(ctx context.Context) error {
	tmcfg, nodeLogger, err := setupNode()
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	tmcfg.Mode = config.ModeSeed

	n, err := node.New(ctx, tmcfg, nodeLogger, nil, nil)
	if err != nil {
		return err
	}
	return n.Start(ctx)
}

func startLightNode(ctx context.Context, logger log.Logger, cfg *Config) error {
	tmcfg, nodeLogger, err := setupNode()
	if err != nil {
		return err
	}

	dbContext := &config.DBContext{ID: "light", Config: tmcfg}
	lightDB, err := config.DefaultDBProvider(dbContext)
	if err != nil {
		return err
	}

	providers := rpcEndpoints(tmcfg.P2P.PersistentPeers)

	c, err := light.NewHTTPClient(
		ctx,
		cfg.ChainID,
		providers[0],
		providers[1:],
		dbs.New(lightDB),
		dashCoreRPCClient,
		light.Logger(nodeLogger),
	)
	if err != nil {
		return err
	}

	rpccfg := rpcserver.DefaultConfig()
	rpccfg.MaxBodyBytes = tmcfg.RPC.MaxBodyBytes
	rpccfg.MaxHeaderBytes = tmcfg.RPC.MaxHeaderBytes
	rpccfg.MaxOpenConnections = tmcfg.RPC.MaxOpenConnections
	// If necessary adjust global WriteTimeout to ensure it's greater than
	// TimeoutBroadcastTxCommit.
	// See https://github.com/tendermint/tendermint/issues/3435
	// Note we don't need to adjust anything if the timeout is already unlimited.
	if rpccfg.WriteTimeout > 0 && rpccfg.WriteTimeout <= tmcfg.RPC.TimeoutBroadcastTxCommit {
		rpccfg.WriteTimeout = tmcfg.RPC.TimeoutBroadcastTxCommit + 1*time.Second
	}

	p, err := lproxy.NewProxy(c, tmcfg.RPC.ListenAddress, providers[0], rpccfg, nodeLogger,
		lrpc.KeyPathFn(lrpc.DefaultMerkleKeyPathFn()))
	if err != nil {
		return err
	}

	logger.Info("Starting proxy...", "laddr", tmcfg.RPC.ListenAddress)
	if err := p.ListenAndServe(ctx); err != http.ErrServerClosed {
		// Error starting or closing listener:
		logger.Error("proxy ListenAndServe", "err", err)
	}

	return nil
}

func setupNode() (*config.Config, log.Logger, error) {
	var tmcfg *config.Config

	home := os.Getenv("TMHOME")
	if home == "" {
		return nil, nil, errors.New("TMHOME not set")
	}

	viper.AddConfigPath(filepath.Join(home, "config"))
	viper.SetConfigName("config")

	if err := viper.ReadInConfig(); err != nil {
		return nil, nil, err
	}

	tmcfg = config.DefaultConfig()

	if err := viper.Unmarshal(tmcfg); err != nil {
		return nil, nil, err
	}

	tmcfg.SetRoot(home)

	if err := tmcfg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("error in config file: %w", err)
	}

	nodeLogger, err := log.NewDefaultLogger(tmcfg.LogFormat, tmcfg.LogLevel)
	if err != nil {
		return nil, nil, err
	}

	return tmcfg, nodeLogger.With("module", "main"), nil
}

// rpcEndpoints takes a list of persistent peers and splits them into a list of rpc endpoints
// using 26657 as the port number
func rpcEndpoints(peers string) []string {
	arr := strings.Split(peers, ",")
	endpoints := make([]string, len(arr))
	for i, v := range arr {
		addr, err := p2p.ParseNodeAddress(v)
		if err != nil {
			panic(err)
		}
		// use RPC port instead
		addr.Port = 26657
		var rpcEndpoint string
		// for ipv6 addresses
		if strings.Contains(addr.Hostname, ":") {
			rpcEndpoint = "http://[" + addr.Hostname + "]:" + fmt.Sprint(addr.Port)
		} else { // for ipv4 addresses
			rpcEndpoint = "http://" + addr.Hostname + ":" + fmt.Sprint(addr.Port)
		}
		endpoints[i] = rpcEndpoint
	}
	return endpoints
}

func setupCoreServer(cfg *Config) (*mockcoreserver.JRPCServer, error) {
	srv := mockcoreserver.NewJRPCServer(cfg.PrivValServer, "/")
	privValKeyPath := filepath.Clean(tmhome + "/" + tmcfg.PrivValidator.Key)
	privValStatePath := filepath.Clean(tmhome + "/" + tmcfg.PrivValidator.State)
	filePV, _ := privval.LoadFilePV(privValKeyPath, privValStatePath)
	// Serve every quorum of the validator set schedule in full, as Dash Core
	// does: state sync authenticates light block validator sets against it.
	validatorSetUpdates, err := cfg.App().ValidatorSetUpdates()
	if err != nil {
		return nil, fmt.Errorf("decoding validator set updates: %w", err)
	}
	quorums, err := mockcoreserver.QuorumsFromValidatorSetUpdates(btcjson.LLMQType_5_60, validatorSetUpdates)
	if err != nil {
		return nil, err
	}
	coreServer := &mockcoreserver.MockCoreServer{
		ChainID:  cfg.ChainID,
		LLMQType: btcjson.LLMQType_5_60,
		FilePV:   filePV,
		Quorums:  quorums,
	}
	srv = mockcoreserver.WithMethods(
		srv,
		mockcoreserver.WithQuorumInfoMethod(coreServer, mockcoreserver.Endless),
		mockcoreserver.WithQuorumSignMethod(coreServer, mockcoreserver.Endless, coreServer.LLMQType),
		mockcoreserver.WithQuorumVerifyMethod(coreServer, mockcoreserver.Endless),
		mockcoreserver.WithMasternodeMethod(coreServer, mockcoreserver.Endless),
		mockcoreserver.WithGetNetworkInfoMethod(coreServer, mockcoreserver.Endless),
		mockcoreserver.WithPingMethod(coreServer, mockcoreserver.Endless),
	)
	return srv, nil
}
