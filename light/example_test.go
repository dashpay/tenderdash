package light_test

import (
	"context"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light"
	httpp "github.com/dashpay/tenderdash/light/provider/http"
	dbs "github.com/dashpay/tenderdash/light/store/db"
	"github.com/dashpay/tenderdash/privval"
	rpctest "github.com/dashpay/tenderdash/rpc/test"
)

// Manually getting light blocks and verifying them.
func TestExampleClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conf, err := rpctest.CreateConfig(t, "ExampleClient_VerifyLightBlockAtHeight")
	if err != nil {
		t.Fatal(err)
	}

	logger, err := log.NewDefaultLogger(log.LogFormatPlain, log.LogLevelInfo)
	if err != nil {
		t.Fatal(err)
	}

	// Start a test application
	app, err := kvstore.NewMemoryApp()
	require.NoError(t, err)

	_, closer, err := rpctest.StartTendermint(ctx, conf, app, rpctest.SuppressStdout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer(ctx) }()

	dbDir := t.TempDir()
	chainID := conf.ChainID()

	primary, err := httpp.New(chainID, conf.RPC.ListenAddress)
	if err != nil {
		t.Fatal(err)
	}

	// give Tendermint time to generate some blocks
	time.Sleep(5 * time.Second)

	_, err = primary.LightBlock(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}

	db, err := dbm.NewGoLevelDB("light-client-db", dbDir)
	if err != nil {
		t.Fatal(err)
	}

	pv, err := privval.LoadOrGenFilePV(conf.PrivValidator.KeyFile(), conf.PrivValidator.StateFile())
	require.NoError(t, err)

	c, err := light.NewClient(
		ctx,
		chainID,
		primary,
		nil,
		dbs.New(db),
		dashcore.NewMockClient(chainID, btcjson.LLMQType_5_60, pv, false),
		light.Logger(log.NewTestingLogger(t)),
	)

	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}()

	// wait for a few more blocks to be produced
	time.Sleep(2 * time.Second)

	// verify the block at height 3
	_, err = c.VerifyLightBlockAtHeight(ctx, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// retrieve light block at height 3
	_, err = c.TrustedLightBlock(3)
	if err != nil {
		t.Fatal(err)
	}

	// update to the latest height
	lb, err := c.Update(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	logger.Info("verified light block", "light-block", lb)
}
