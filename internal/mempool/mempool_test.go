package mempool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	sync "github.com/sasha-s/go-deadlock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/code"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/types"
)

// application extends the KV store application by overriding CheckTx to provide
// transaction priority based on the value in the key/value pair.
type application struct {
	*kvstore.Application
}

type testTx struct {
	tx       types.Tx
	priority int64
}

func (app *application) CheckTx(_ context.Context, req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	var (
		priority int64
		sender   string
	)

	// infer the priority from the raw transaction value (sender=key=value)
	parts := bytes.Split(req.Tx, []byte("="))
	if len(parts) == 3 {
		v, err := strconv.ParseInt(string(parts[2]), 10, 64)
		if err != nil {
			return &abci.ResponseCheckTx{
				Priority:  priority,
				Code:      100,
				GasWanted: 1,
			}, nil
		}

		priority = v
		sender = string(parts[0])
	} else {
		return &abci.ResponseCheckTx{
			Priority:  priority,
			Code:      101,
			GasWanted: 1,
		}, nil
	}

	return &abci.ResponseCheckTx{
		Priority:  priority,
		Sender:    sender,
		Code:      code.CodeTypeOK,
		GasWanted: 1,
	}, nil
}

func setup(t testing.TB, app abciclient.Client, cacheSize int, options ...TxMempoolOption) *TxMempool {
	t.Helper()

	logger := log.NewNopLogger()

	cfg, err := config.ResetTestRoot(t.TempDir(), strings.ReplaceAll(t.Name(), "/", "|"))
	require.NoError(t, err)
	cfg.Mempool.CacheSize = cacheSize

	t.Cleanup(func() { os.RemoveAll(cfg.RootDir) })

	return NewTxMempool(logger.With("test", t.Name()), cfg.Mempool, app, options...)
}

// mustCheckTx invokes txmp.CheckTx for the given transaction and waits until
// its callback has finished executing. It fails t if CheckTx fails.
func mustCheckTx(ctx context.Context, t *testing.T, txmp *TxMempool, spec string) {
	done := make(chan struct{})
	if err := txmp.CheckTx(ctx, []byte(spec), func(*abci.ResponseCheckTx) {
		close(done)
	}, TxInfo{}); err != nil {
		t.Fatalf("CheckTx for %q failed: %v", spec, err)
	}
	<-done
}

func checkTxs(ctx context.Context, t *testing.T, txmp *TxMempool, numTxs int, peerID uint16) []testTx {
	checkTxCallback := func(resp *abci.ResponseCheckTx) {
		require.NotNil(t, resp)
		assert.Equal(t, abci.CodeTypeOK, resp.Code, string(resp.Data))
	}

	txs := make([]testTx, numTxs)
	txInfo := TxInfo{SenderID: peerID}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < numTxs; i++ {
		prefix := make([]byte, 20)
		_, err := rng.Read(prefix)
		require.NoError(t, err)

		priority := int64(rng.Intn(9999-1000) + 1000)

		txs[i] = testTx{
			tx:       []byte(fmt.Sprintf("sender-%d-%d=%X=%d", i, peerID, prefix, priority)),
			priority: priority,
		}
		require.NoError(t, txmp.CheckTx(ctx, txs[i].tx, checkTxCallback, txInfo))
	}

	return txs
}

func convertTex(in []testTx) types.Txs {
	out := make([]types.Tx, len(in))

	for idx := range in {
		out[idx] = in[idx].tx
	}

	return out
}

func TestTxMempool_TxsAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 0)
	txmp.EnableTxsAvailable()

	ensureNoTxFire := func() {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-txmp.TxsAvailable():
			require.Fail(t, "unexpected transactions event")
		case <-timer.C:
		}
	}

	ensureTxFire := func() {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-txmp.TxsAvailable():
		case <-timer.C:
			require.Fail(t, "expected transactions event")
		}
	}

	// ensure no event as we have not executed any transactions yet
	ensureNoTxFire()

	// Execute CheckTx for some transactions and ensure TxsAvailable only fires
	// once.
	txs := checkTxs(ctx, t, txmp, 100, 0)
	ensureTxFire()
	ensureNoTxFire()

	rawTxs := make([]types.Tx, len(txs))
	for i, tx := range txs {
		rawTxs[i] = tx.tx
	}

	responses := make([]*abci.ExecTxResult, len(rawTxs[:50]))
	for i := 0; i < len(responses); i++ {
		responses[i] = &abci.ExecTxResult{Code: abci.CodeTypeOK}
	}

	// commit half the transactions and ensure we fire an event
	txmp.Lock()
	require.NoError(t, txmp.Update(ctx, 1, rawTxs[:50], responses, nil, nil, true))
	txmp.Unlock()
	ensureTxFire()
	ensureNoTxFire()

	// Execute CheckTx for more transactions and ensure we do not fire another
	// event as we're still on the same height (1).
	_ = checkTxs(ctx, t, txmp, 100, 0)
	ensureNoTxFire()
}

func TestTxMempool_Size(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 0)
	txs := checkTxs(ctx, t, txmp, 100, 0)
	require.Equal(t, len(txs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())

	rawTxs := make([]types.Tx, len(txs))
	for i, tx := range txs {
		rawTxs[i] = tx.tx
	}

	responses := make([]*abci.ExecTxResult, len(rawTxs[:50]))
	for i := 0; i < len(responses); i++ {
		responses[i] = &abci.ExecTxResult{Code: abci.CodeTypeOK}
	}

	txmp.Lock()
	require.NoError(t, txmp.Update(ctx, 1, rawTxs[:50], responses, nil, nil, true))
	txmp.Unlock()

	require.Equal(t, len(rawTxs)/2, txmp.Size())
	require.Equal(t, int64(2850), txmp.SizeBytes())
}

func TestTxMempool_Eviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 1000)
	txmp.config.Size = 5
	txmp.config.MaxTxsBytes = 60
	txExists := func(spec string) bool {
		txmp.Lock()
		defer txmp.Unlock()
		key := types.Tx(spec).Key()
		_, ok := txmp.txByKey[key]
		return ok
	}
	t.Cleanup(client.Wait)

	// A transaction bigger than the mempool should be rejected even when there
	// are slots available.
	mustCheckTx(ctx, t, txmp, "big=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef=1")
	require.Equal(t, 0, txmp.Size())

	// Nearly-fill the mempool with a low-priority transaction, to show that it
	// is evicted even when slots are available for a higher-priority tx.
	const bigTx = "big=0123456789abcdef0123456789abcdef0123456789abcdef01234=2"
	mustCheckTx(ctx, t, txmp, bigTx)
	require.Equal(t, 1, txmp.Size()) // bigTx is the only element
	require.True(t, txExists(bigTx))
	require.Equal(t, int64(len(bigTx)), txmp.SizeBytes())

	// The next transaction should evict bigTx, because it is higher priority
	// but does not fit on size.
	mustCheckTx(ctx, t, txmp, "key1=0000=25")
	require.True(t, txExists("key1=0000=25"))
	require.False(t, txExists(bigTx))
	require.False(t, txmp.cache.Has([]byte(bigTx)))
	require.Equal(t, int64(len("key1=0000=25")), txmp.SizeBytes())

	// Now fill up the rest of the slots with other transactions.
	mustCheckTx(ctx, t, txmp, "key2=0001=5")
	mustCheckTx(ctx, t, txmp, "key3=0002=10")
	mustCheckTx(ctx, t, txmp, "key4=0003=3")
	mustCheckTx(ctx, t, txmp, "key5=0004=3")

	// A new transaction with low priority should be discarded.
	mustCheckTx(ctx, t, txmp, "key6=0005=1")
	require.False(t, txExists("key6=0005=1"))

	// A new transaction with higher priority should evict key5, which is the
	// newest of the two transactions with lowest priority.
	mustCheckTx(ctx, t, txmp, "key7=0006=7")
	require.True(t, txExists("key7=0006=7"))  // new transaction added
	require.False(t, txExists("key5=0004=3")) // newest low-priority tx evicted
	require.True(t, txExists("key4=0003=3"))  // older low-priority tx retained

	// Another new transaction evicts the other low-priority element.
	mustCheckTx(ctx, t, txmp, "key8=0007=20")
	require.True(t, txExists("key8=0007=20"))
	require.False(t, txExists("key4=0003=3"))

	// Now the lowest-priority tx is 5, so that should be the next to go.
	mustCheckTx(ctx, t, txmp, "key9=0008=9")
	require.True(t, txExists("key9=0008=9"))
	require.False(t, txExists("k3y2=0001=5"))

	// Add a transaction that requires eviction of multiple lower-priority
	// entries, in order to fit the size of the element.
	mustCheckTx(ctx, t, txmp, "key10=0123456789abcdef=11") // evict 10, 9, 7; keep 25, 20, 11
	require.True(t, txExists("key1=0000=25"))
	require.True(t, txExists("key8=0007=20"))
	require.True(t, txExists("key10=0123456789abcdef=11"))
	require.False(t, txExists("key3=0002=10"))
	require.False(t, txExists("key9=0008=9"))
	require.False(t, txExists("key7=0006=7"))
}

func TestTxMempool_Flush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 0)
	txs := checkTxs(ctx, t, txmp, 100, 0)
	require.Equal(t, len(txs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())

	rawTxs := make([]types.Tx, len(txs))
	for i, tx := range txs {
		rawTxs[i] = tx.tx
	}

	responses := make([]*abci.ExecTxResult, len(rawTxs[:50]))
	for i := 0; i < len(responses); i++ {
		responses[i] = &abci.ExecTxResult{Code: abci.CodeTypeOK}
	}

	txmp.Lock()
	require.NoError(t, txmp.Update(ctx, 1, rawTxs[:50], responses, nil, nil, true))
	txmp.Unlock()

	txmp.Flush()
	require.Zero(t, txmp.Size())
	require.Equal(t, int64(0), txmp.SizeBytes())
}

func TestTxMempool_ReapMaxBytesMaxGas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 0)
	tTxs := checkTxs(ctx, t, txmp, 100, 0) // all txs request 1 gas unit
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())

	txMap := make(map[types.TxKey]testTx)
	priorities := make([]int64, len(tTxs))
	for i, tTx := range tTxs {
		txMap[tTx.tx.Key()] = tTx
		priorities[i] = tTx.priority
	}

	sort.Slice(priorities, func(i, j int) bool {
		// sort by priority, i.e. decreasing order
		return priorities[i] > priorities[j]
	})

	ensurePrioritized := func(reapedTxs types.Txs) {
		reapedPriorities := make([]int64, len(reapedTxs))
		for i, rTx := range reapedTxs {
			reapedPriorities[i] = txMap[rTx.Key()].priority
		}

		require.Equal(t, priorities[:len(reapedPriorities)], reapedPriorities)
	}

	// reap by gas capacity only
	reapedTxs := txmp.ReapMaxBytesMaxGas(-1, 50)
	ensurePrioritized(reapedTxs)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())
	require.Len(t, reapedTxs, 50)

	// reap by transaction bytes only
	reapedTxs = txmp.ReapMaxBytesMaxGas(1000, -1)
	ensurePrioritized(reapedTxs)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())
	require.GreaterOrEqual(t, len(reapedTxs), 16)

	// Reap by both transaction bytes and gas, where the size yields 31 reaped
	// transactions and the gas limit reaps 25 transactions.
	reapedTxs = txmp.ReapMaxBytesMaxGas(1500, 30)
	ensurePrioritized(reapedTxs)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())
	require.Len(t, reapedTxs, 25)
}

func TestTxMempool_ReapMaxTxs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 0)
	tTxs := checkTxs(ctx, t, txmp, 100, 0)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())

	txMap := make(map[types.TxKey]testTx)
	priorities := make([]int64, len(tTxs))
	for i, tTx := range tTxs {
		txMap[tTx.tx.Key()] = tTx
		priorities[i] = tTx.priority
	}

	sort.Slice(priorities, func(i, j int) bool {
		// sort by priority, i.e. decreasing order
		return priorities[i] > priorities[j]
	})

	ensurePrioritized := func(reapedTxs types.Txs) {
		reapedPriorities := make([]int64, len(reapedTxs))
		for i, rTx := range reapedTxs {
			reapedPriorities[i] = txMap[rTx.Key()].priority
		}

		require.Equal(t, priorities[:len(reapedPriorities)], reapedPriorities)
	}

	// reap all transactions
	reapedTxs := txmp.ReapMaxTxs(-1)
	ensurePrioritized(reapedTxs)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())
	require.Len(t, reapedTxs, len(tTxs))

	// reap a single transaction
	reapedTxs = txmp.ReapMaxTxs(1)
	ensurePrioritized(reapedTxs)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())
	require.Len(t, reapedTxs, 1)

	// reap half of the transactions
	reapedTxs = txmp.ReapMaxTxs(len(tTxs) / 2)
	ensurePrioritized(reapedTxs)
	require.Equal(t, len(tTxs), txmp.Size())
	require.Equal(t, int64(5690), txmp.SizeBytes())
	require.Len(t, reapedTxs, len(tTxs)/2)
}

func TestTxMempool_CheckTxExceedsMaxSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)
	txmp := setup(t, client, 0)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	tx := make([]byte, txmp.config.MaxTxBytes+1)
	_, err := rng.Read(tx)
	require.NoError(t, err)

	require.Error(t, txmp.CheckTx(ctx, tx, nil, TxInfo{SenderID: 0}))

	tx = make([]byte, txmp.config.MaxTxBytes-1)
	_, err = rng.Read(tx)
	require.NoError(t, err)

	require.NoError(t, txmp.CheckTx(ctx, tx, nil, TxInfo{SenderID: 0}))
}

func TestTxMempool_CheckTxSamePeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 100)
	peerID := uint16(1)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	prefix := make([]byte, 20)
	_, err := rng.Read(prefix)
	require.NoError(t, err)

	tx := []byte(fmt.Sprintf("sender-0=%X=%d", prefix, 50))

	require.NoError(t, txmp.CheckTx(ctx, tx, nil, TxInfo{SenderID: peerID}))
	require.Error(t, txmp.CheckTx(ctx, tx, nil, TxInfo{SenderID: peerID}))
}

func TestTxMempool_CheckTxSameSender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 100)
	peerID := uint16(1)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	prefix1 := make([]byte, 20)
	_, err := rng.Read(prefix1)
	require.NoError(t, err)

	prefix2 := make([]byte, 20)
	_, err = rng.Read(prefix2)
	require.NoError(t, err)

	tx1 := []byte(fmt.Sprintf("sender-0=%X=%d", prefix1, 50))
	tx2 := []byte(fmt.Sprintf("sender-0=%X=%d", prefix2, 50))

	require.NoError(t, txmp.CheckTx(ctx, tx1, nil, TxInfo{SenderID: peerID}))
	require.Equal(t, 1, txmp.Size())
	require.NoError(t, txmp.CheckTx(ctx, tx2, nil, TxInfo{SenderID: peerID}))
	require.Equal(t, 1, txmp.Size())
}

func TestTxMempool_ConcurrentTxs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 100)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	checkTxDone := make(chan struct{})

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		for i := 0; i < 20; i++ {
			_ = checkTxs(ctx, t, txmp, 100, 0)
			dur := rng.Intn(1000-500) + 500
			time.Sleep(time.Duration(dur) * time.Millisecond)
		}

		wg.Done()
		close(checkTxDone)
	}()

	wg.Add(1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer wg.Done()

		var height int64 = 1

		for range ticker.C {
			reapedTxs := txmp.ReapMaxTxs(200)
			if len(reapedTxs) > 0 {
				responses := make([]*abci.ExecTxResult, len(reapedTxs))
				for i := 0; i < len(responses); i++ {
					var code uint32

					if i%10 == 0 {
						code = 100
					} else {
						code = abci.CodeTypeOK
					}

					responses[i] = &abci.ExecTxResult{Code: code}
				}

				txmp.Lock()
				require.NoError(t, txmp.Update(ctx, height, reapedTxs, responses, nil, nil, true))
				txmp.Unlock()

				height++
			} else {
				// only return once we know we finished the CheckTx loop
				select {
				case <-checkTxDone:
					return
				default:
				}
			}
		}
	}()

	wg.Wait()
	require.Zero(t, txmp.Size())
	require.Zero(t, txmp.SizeBytes())
}

func TestTxMempool_ExpiredTxs_NumBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 500)
	txmp.height = 100
	txmp.config.TTLNumBlocks = 10

	tTxs := checkTxs(ctx, t, txmp, 100, 0)
	require.Equal(t, len(tTxs), txmp.Size())

	// reap 5 txs at the next height -- no txs should expire
	reapedTxs := txmp.ReapMaxTxs(5)
	responses := make([]*abci.ExecTxResult, len(reapedTxs))
	for i := 0; i < len(responses); i++ {
		responses[i] = &abci.ExecTxResult{Code: abci.CodeTypeOK}
	}

	txmp.Lock()
	require.NoError(t, txmp.Update(ctx, txmp.height+1, reapedTxs, responses, nil, nil, true))
	txmp.Unlock()

	require.Equal(t, 95, txmp.Size())

	// check more txs at height 101
	_ = checkTxs(ctx, t, txmp, 50, 1)
	require.Equal(t, 145, txmp.Size())

	// Reap 5 txs at a height that would expire all the transactions from before
	// the previous Update (height 100).
	//
	// NOTE: When we reap txs below, we do not know if we're picking txs from the
	// initial CheckTx calls or from the second round of CheckTx calls. Thus, we
	// cannot guarantee that all 95 txs are remaining that should be expired and
	// removed. However, we do know that that at most 95 txs can be expired and
	// removed.
	reapedTxs = txmp.ReapMaxTxs(5)
	responses = make([]*abci.ExecTxResult, len(reapedTxs))
	for i := 0; i < len(responses); i++ {
		responses[i] = &abci.ExecTxResult{Code: abci.CodeTypeOK}
	}

	txmp.Lock()
	require.NoError(t, txmp.Update(ctx, txmp.height+10, reapedTxs, responses, nil, nil, true))
	txmp.Unlock()

	require.GreaterOrEqual(t, txmp.Size(), 45)
}

func TestTxMempool_CheckTxPostCheckError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cases := []struct {
		name string
		err  error
	}{
		{
			name: "error",
			err:  errors.New("test error"),
		},
		{
			name: "no error",
			err:  nil,
		},
	}
	for _, tc := range cases {
		testCase := tc
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			client := abciclient.NewLocalClient(log.NewNopLogger(), &application{Application: mustKvStore(t)})
			if err := client.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Wait)

			postCheckFn := func(_ types.Tx, _ *abci.ResponseCheckTx) error {
				return testCase.err
			}
			txmp := setup(t, client, 0, WithPostCheck(postCheckFn))
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			tx := make([]byte, txmp.config.MaxTxBytes-1)
			_, err := rng.Read(tx)
			require.NoError(t, err)

			callback := func(res *abci.ResponseCheckTx) {
				expectedErrString := ""
				if testCase.err != nil {
					expectedErrString = testCase.err.Error()
					require.Equal(t, expectedErrString, txmp.postCheck(tx, res).Error())
				} else {
					require.Equal(t, nil, txmp.postCheck(tx, res))
				}
			}
			if testCase.err == nil {
				require.NoError(t, txmp.CheckTx(ctx, tx, callback, TxInfo{SenderID: 0}))
			} else {
				err = txmp.CheckTx(ctx, tx, callback, TxInfo{SenderID: 0})
				require.EqualError(t, err, "test error")
			}
		})
	}
}

// TestTxMempool_OneRecheckTxAtTime checks if previous recheckTransactions task is canceled when another one is started.
//
// Given mempool with some transactions AND app that processes CheckTX very slowly,
// when we call recheckTransactions() twice,
// then first recheckTransactions task is canceled and second one starts from the beginning.
func TestTxMempool_OneRecheckTxAtTime(t *testing.T) {
	// SETUP
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)

	// num of parallel tasks started in recheckTransactions; this is how many
	// txs will be processed as a minimum
	numRecheckTasks := 2 * runtime.NumCPU()
	numTxs := 3 * numRecheckTasks

	app := mocks.NewApplication(t)
	var (
		checkTxCounter   atomic.Uint32
		recheckTxBlocker sync.Mutex
	)
	// app will wait on recheckTxBlocker until we unblock it
	app.On("CheckTx", mock.Anything, mock.Anything).Return(&abci.ResponseCheckTx{
		Priority: 1,
		Code:     abci.CodeTypeOK}, nil).
		Run(func(_ mock.Arguments) {
			// increase counter before locking, so we can check if it was called
			checkTxCounter.Add(1)
			recheckTxBlocker.Lock()
			defer recheckTxBlocker.Unlock()
		})

	client := abciclient.NewLocalClient(log.NewNopLogger(), app)
	cfg := config.TestConfig()
	mp := NewTxMempool(logger, cfg.Mempool, client)
	// add some txs to mempool
	for i := 0; i < numTxs; i++ {
		err := mp.addNewTransaction(randomTx(), &abci.ResponseCheckTx{Code: abci.CodeTypeOK, GasWanted: 1, Priority: int64(i + 1)})
		require.NoError(t, err)
	}

	// TEST

	// block checkTx until we unblock it
	recheckTxBlocker.Lock()
	// start recheckTransactions in the background; it should process exactly one tx per recheck task
	mp.recheckTransactions(ctx)
	assert.Eventually(t,
		func() bool { return checkTxCounter.Load() == uint32(numRecheckTasks) },
		200*time.Millisecond, 10*time.Millisecond,
		"1st run: processed %d txs, expected %d", checkTxCounter.Load(), numRecheckTasks)

	// another recheck should cancel the first run and start from the beginning , but pending checkTx ops should finish
	mp.recheckTransactions(ctx)
	// unlock the app; this should finish all started rechecks, but not continue with rechecks from 1st run
	recheckTxBlocker.Unlock()
	// Ensure that all goroutines/tasks have finished
	assert.Eventually(t, func() bool { return uint32(numRecheckTasks+numTxs) == checkTxCounter.Load() },
		200*time.Millisecond, 10*time.Millisecond,
		"num of txs mismatch: got %d, expected %d", checkTxCounter.Load(), numRecheckTasks+numTxs)

	// let's give it some more time and ensure we don't process any further txs
	if !testing.Short() {
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, uint32(numRecheckTasks+numTxs), checkTxCounter.Load())
	}
}

func randomTx() *WrappedTx {
	tx := tmrand.Bytes(10)
	return &WrappedTx{
		tx:        tx,
		height:    1,
		timestamp: time.Now(),
		gasWanted: 1,
		priority:  1,
		peers:     map[uint16]bool{},
	}
}

func mustKvStore(t *testing.T, opts ...kvstore.OptFunc) *kvstore.Application {
	opts = append(opts, kvstore.WithLogger(log.NewTestingLogger(t).With("module", "kvstore")))
	app, err := kvstore.NewMemoryApp(opts...)
	require.NoError(t, err)
	return app
}
