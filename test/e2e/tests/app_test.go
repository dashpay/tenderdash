package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	db "github.com/cometbft/cometbft-db"
	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/code"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/rpc/client/http"
	e2eapp "github.com/dashpay/tenderdash/test/e2e/app"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/types"
)

const (
	randomSeed = 4827085738
)

// TestApp_ProposeNextBlockImmediately checks that a network whose nodes never
// create empty blocks on their own keeps producing blocks when the application
// sets ResponseFinalizeBlock.propose_next_block_immediately. Without the hint a
// proposer waits for mempool transactions, so a block above the initial height
// that carries none can only come from it. The e2e application adds its own
// extensionSum transaction to every proposal, so "none" means no transaction
// other than that one. The runner keeps the chain running for several blocks
// after it stops the load generator, which is where these blocks appear; the
// test also watches the chain grow while nothing submits transactions.
func TestApp_ProposeNextBlockImmediately(t *testing.T) {
	testnet := loadTestnet(t)
	if testnet.CreateEmptyBlocks || !testnet.ProposeNextBlockImmediately {
		t.Skip("testnet does not combine create_empty_blocks = false with propose_next_block_immediately")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	extTxPrefix := []byte(e2eapp.VoteExtensionKey + "=")
	blocksWithoutMempoolTxs := 0
	for _, block := range fetchBlockChain(ctx, t) {
		// The initial height never waits for transactions, so it does not count.
		if block.Height <= testnet.InitialHeight {
			continue
		}
		mempoolTxs := 0
		for _, tx := range block.Txs {
			if !bytes.HasPrefix(tx, extTxPrefix) {
				mempoolTxs++
			}
		}
		if mempoolTxs == 0 {
			blocksWithoutMempoolTxs++
		}
	}
	require.Greater(t, blocksWithoutMempoolTxs, 0,
		"no block above height %d without mempool transactions: the propose_next_block_immediately hint did not drive block production",
		testnet.InitialHeight)

	// Nothing submits transactions while this test runs, so any growth comes from the hint.
	archiveNodes := testnet.ArchiveNodes()
	require.NotEmpty(t, archiveNodes)
	client, err := archiveNodes[0].Client()
	require.NoError(t, err)
	status, err := client.Status(ctx)
	require.NoError(t, err)
	startHeight := status.SyncInfo.LatestBlockHeight
	require.Eventually(t, func() bool {
		status, err := client.Status(ctx)
		return err == nil && status.SyncInfo.LatestBlockHeight > startHeight
	}, 30*time.Second, 500*time.Millisecond,
		"chain did not grow past height %d without transactions", startHeight)
}

// Tests that any initial state given in genesis has made it into the app.
func TestApp_InitialState(t *testing.T) {
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {

		client, err := node.Client()
		require.NoError(t, err)
		state := kvstore.NewKvState(db.NewMemDB(), 0)
		err = state.Load(bytes.NewBufferString(node.Testnet.InitialState))
		require.NoError(t, err)
		iter, err := state.Iterator(nil, nil)
		require.NoError(t, err)

		for iter.Valid() {
			k := iter.Key()
			v := iter.Value()
			resp, err := client.ABCIQuery(ctx, "", k)
			require.NoError(t, err)
			assert.Equal(t, k, resp.Response.Key)
			assert.Equal(t, v, resp.Response.Value)

			iter.Next()
		}
	})
}

// Tests that the app hash (as reported by the app) matches the last
// block and the node sync status.
func TestApp_Hash(t *testing.T) {
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
		client, err := node.Client()
		require.NoError(t, err)

		info, err := client.ABCIInfo(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, info.Response.LastBlockAppHash, "expected app to return app hash")

		// In same-block execution, the app hash is stored in the same block
		blockHeight := info.Response.LastBlockHeight

		require.Eventually(t, func() bool {
			status, err := client.Status(ctx)
			require.NoError(t, err)
			require.NotZero(t, status.SyncInfo.LatestBlockHeight)
			return status.SyncInfo.LatestBlockHeight >= blockHeight
		}, 60*time.Second, 500*time.Millisecond)

		block, err := client.Block(ctx, &blockHeight)
		require.NoError(t, err)
		require.Equal(t, blockHeight, block.Block.Height)
		require.EqualValues(t,
			info.Response.LastBlockAppHash,
			block.Block.AppHash.Bytes(),
			"app hash does not match last block's app hash at height %d", blockHeight)
	})
}

// Tests that the app and blockstore have and report the same height.
func TestApp_Height(t *testing.T) {
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
		client, err := node.Client()
		require.NoError(t, err)
		info, err := client.ABCIInfo(ctx)
		require.NoError(t, err)
		require.NotZero(t, info.Response.LastBlockHeight)

		status, err := client.Status(ctx)
		require.NoError(t, err)
		require.NotZero(t, status.SyncInfo.LatestBlockHeight)

		block, err := client.Block(ctx, &info.Response.LastBlockHeight)
		require.NoError(t, err)

		require.Equal(t, info.Response.LastBlockHeight, block.Block.Height)

		require.True(t, status.SyncInfo.LatestBlockHeight >= info.Response.LastBlockHeight,
			"status out of sync with application")
	})
}

// Tests that we can set a value and retrieve it.
func TestApp_Tx(t *testing.T) {
	type broadcastFunc func(context.Context, types.Tx) error

	testCases := []struct {
		Name        string
		WaitTime    time.Duration
		BroadcastTx func(client *http.HTTP) broadcastFunc
		ShouldSkip  bool
	}{
		{
			Name:     "sync",
			WaitTime: time.Minute,
			BroadcastTx: func(client *http.HTTP) broadcastFunc {
				return func(ctx context.Context, tx types.Tx) error {
					_, err := client.BroadcastTxSync(ctx, tx)
					return err
				}
			},
		},
		{
			Name:     "flushMempool",
			WaitTime: 15 * time.Second,
			// TODO: turn this check back on if it can
			// return reliably. Currently these calls have
			// a hard timeout of 10s (server side
			// configured). The Sync check is probably
			// safe.
			ShouldSkip: true,
			BroadcastTx: func(client *http.HTTP) broadcastFunc {
				return func(ctx context.Context, tx types.Tx) error {
					_, err := client.BroadcastTxCommit(ctx, tx)
					return err
				}
			},
		},
		{
			Name:     "Async",
			WaitTime: 90 * time.Second,
			// TODO: turn this check back on if there's a
			// way to avoid failures in the case that the
			// transaction doesn't make it into the
			// mempool. (retries?)
			ShouldSkip: true,
			BroadcastTx: func(client *http.HTTP) broadcastFunc {
				return func(ctx context.Context, tx types.Tx) error {
					_, err := client.BroadcastTxAsync(ctx, tx)
					return err
				}
			},
		},
	}

	r := rand.New(rand.NewSource(randomSeed))
	// rMtx guards r: testNode runs node subtests in parallel, and *rand.Rand is
	// not safe for concurrent use.
	var rMtx sync.Mutex
	for idx, test := range testCases {
		if test.ShouldSkip {
			continue
		}
		t.Run(test.Name, func(t *testing.T) {
			test := testCases[idx]
			testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
				client, err := node.Client()
				require.NoError(t, err)

				key := fmt.Sprintf("testapp-tx-%v", node.Name)
				rMtx.Lock()
				value := tmrand.StrFromSource(r, 32)
				rMtx.Unlock()
				tx := types.Tx(fmt.Sprintf("%v=%v", key, value))

				err = test.BroadcastTx(client)(ctx, tx)
				require.NoError(t, err)

				hash := tx.Hash()

				require.Eventuallyf(t, func() bool {
					txResp, err := client.Tx(ctx, hash, false)
					return err == nil && bytes.Equal(txResp.Tx, tx)
				},
					test.WaitTime, // timeout
					time.Second,   // interval
					"submitted tx %X wasn't committed after %v",
					hash, test.WaitTime,
				)

				abciResp, err := client.ABCIQuery(ctx, "", []byte(key))
				require.NoError(t, err)
				assert.Equal(t, code.CodeTypeOK, abciResp.Response.Code)
				assert.Equal(t, key, string(abciResp.Response.Key))
				assert.Equal(t, value, string(abciResp.Response.Value))
			})

		})

	}

}

// Given transactions which take more than the block size,
// when I submit them to the node,
// then all transactions should be committed across multiple blocks.
func TestApp_TxTooBig(t *testing.T) {
	/// timeout for broadcast to single node
	const broadcastTimeout = 10 * time.Second
	/// Timeout to read response from single node
	const readTimeout = 1 * time.Second
	/// Time to process whole mempool
	const includeInBlockTimeout = 120 * time.Second

	mainCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testnet := loadTestnet(t)
	nodes := testnet.Nodes

	if name := os.Getenv("E2E_NODE"); name != "" {
		node := testnet.LookupNode(name)
		require.NotNil(t, node, "node %q not found in testnet %q", name, testnet.Name)
		nodes = []*e2e.Node{node}
	} else {
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].Name < nodes[j].Name
		})
	}

	start := time.Now()

	/// Send more txs than we can fit into block

	ctx, cancel := context.WithTimeout(mainCtx, broadcastTimeout)
	defer cancel()

	if ctx.Err() != nil {
		t.Fatalf("context canceled before broadcasting to all nodes")
	}
	// find first non-stateless node
	var node *e2e.Node
	for _, node = range nodes {
		if !node.Stateless() {
			break
		}
	}

	t.Logf("broadcasting to %s", node.Name)

	session := rand.Int63()

	client, err := node.Client()
	require.NoError(t, err)

	// FIXME: ConsensusParams is broken for last height, this is just workaround
	status, err := client.Status(ctx)
	assert.NoError(t, err)
	cp, err := client.ConsensusParams(ctx, &status.SyncInfo.LatestBlockHeight)
	assert.NoError(t, err)

	// ensure we have more txs than fits the block
	TxPayloadSize := int(cp.ConsensusParams.Block.MaxBytes / 100) // 1% of block size
	numTxs := 101

	tx := make(types.Tx, TxPayloadSize) // first tx is just zeros

	pending := make(map[string]tmbytes.HexBytes, numTxs)
	var key string

	for i := 0; i < numTxs; i++ {
		key = fmt.Sprintf("testapp-big-tx-%v-%08x-%d=", node.Name, session, i)
		copy(tx, key)

		payloadOffset := len(tx) - 8 // where we put the `i` into the payload
		assert.Greater(t, payloadOffset, len(key))

		big.NewInt(int64(i)).FillBytes(tx[payloadOffset:])
		assert.Len(t, tx, TxPayloadSize)

		hash := tx.Hash()
		pending[string(hash)] = hash

		_, err = client.BroadcastTxAsync(ctx, tx)

		require.NoError(t, err, "failed to broadcast tx %06x", i)
	}

	t.Logf("submitted txs in %s", time.Since(start).String())

	var minHeight, maxHeight int64
	// Submission order does not determine arrival order at the proposer.
	require.Eventuallyf(t, func() bool {
		for key, hash := range pending {
			ctx, cancel := context.WithTimeout(mainCtx, readTimeout)
			resp, err := client.Tx(ctx, hash, false)
			cancel()
			if err != nil {
				return false
			}
			assert.EqualValues(t, hash, resp.Tx.Hash())
			if minHeight == 0 || resp.Height < minHeight {
				minHeight = resp.Height
			}
			maxHeight = max(maxHeight, resp.Height)
			delete(pending, key)
		}
		return true
	}, includeInBlockTimeout, time.Second,
		"submitted transactions were not committed after %s", includeInBlockTimeout)

	t.Logf("%d transactions committed across heights %d–%d", numTxs, minHeight, maxHeight)
	assert.Less(t, minHeight, maxHeight, "transactions exceeding the block size must span multiple blocks")
}

// Tests that the app version in most recent block is set to height of the block.
// Requires kvstore.WithEnforceVersionToHeight() to be enabled.
func TestApp_AppVersion(t *testing.T) {
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
		client, err := node.Client()
		require.NoError(t, err)
		info, err := client.ABCIInfo(ctx)
		require.NoError(t, err)
		require.NotZero(t, info.Response.LastBlockHeight)

		block, err := client.Block(ctx, &info.Response.LastBlockHeight)
		require.NoError(t, err)

		require.Equal(t, info.Response.LastBlockHeight, block.Block.Height)
		require.EqualValues(t, block.Block.Height, block.Block.Version.App)
	})
}
