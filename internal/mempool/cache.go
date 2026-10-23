package mempool

import (
	"bytes"
	"container/list"

	sync "github.com/sasha-s/go-deadlock"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/types"
)

// maxCachedRejectionBytes caps the Codespace, Info and Data kept per rejected
// transaction, which bounds the extra cache memory to cache size times this value.
const maxCachedRejectionBytes = 2048

// TxCache defines an interface for raw transaction caching in a mempool.
// Currently, a TxCache does not allow direct reading or getting of transaction
// values. A TxCache is used primarily to push transactions and removing
// transactions. Pushing via Push returns a boolean telling the caller if the
// transaction already exists in the cache or not.
type TxCache interface {
	// Reset resets the cache to an empty state.
	Reset()

	// Push adds the given raw transaction to the cache and returns true if it was
	// newly added. Otherwise, it returns false.
	Push(tx types.Tx) bool

	// Remove removes the given raw transaction from the cache.
	Remove(tx types.Tx)

	// Has reports whether tx is present in the cache. Checking for presence is
	// not treated as an access of the value.
	Has(tx types.Tx) bool

	// SetRejection records the CheckTx response that rejected tx, so that it can
	// be reported again when tx is resubmitted. It does nothing if tx is not in
	// the cache, is marked as committed, or the response exceeds
	// maxCachedRejectionBytes.
	SetRejection(tx types.Tx, res *abci.ResponseCheckTx)

	// MarkCommitted drops the rejection recorded for tx and makes the cache
	// ignore further ones for as long as tx stays in it.
	MarkCommitted(tx types.Tx)

	// Rejection returns a copy of the response recorded with SetRejection, or
	// nil if there is none.
	Rejection(tx types.Tx) *abci.ResponseCheckTx
}

var _ TxCache = (*LRUTxCache)(nil)

// LRUTxCache maintains a thread-safe LRU cache of raw transactions. The cache
// only stores the hash of the raw transaction and, for rejected transactions,
// the response that rejected them.
type LRUTxCache struct {
	mtx        sync.Mutex
	size       int
	cacheMap   map[types.TxKey]*list.Element
	list       *list.List
	rejections map[types.TxKey]*abci.ResponseCheckTx
	committed  map[types.TxKey]struct{}
}

func NewLRUTxCache(cacheSize int) *LRUTxCache {
	return &LRUTxCache{
		size:       cacheSize,
		cacheMap:   make(map[types.TxKey]*list.Element, cacheSize),
		list:       list.New(),
		rejections: make(map[types.TxKey]*abci.ResponseCheckTx),
		committed:  make(map[types.TxKey]struct{}),
	}
}

// GetList returns the underlying linked-list that backs the LRU cache. Note,
// this should be used for testing purposes only!
func (c *LRUTxCache) GetList() *list.List {
	return c.list
}

func (c *LRUTxCache) Reset() {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cacheMap = make(map[types.TxKey]*list.Element, c.size)
	c.rejections = make(map[types.TxKey]*abci.ResponseCheckTx)
	c.committed = make(map[types.TxKey]struct{})
	c.list.Init()
}

func (c *LRUTxCache) Push(tx types.Tx) bool {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	key := tx.Key()

	moved, ok := c.cacheMap[key]
	if ok {
		c.list.MoveToBack(moved)
		return false
	}

	if c.list.Len() >= c.size {
		front := c.list.Front()
		if front != nil {
			frontKey := front.Value.(types.TxKey)
			delete(c.cacheMap, frontKey)
			delete(c.rejections, frontKey)
			delete(c.committed, frontKey)
			c.list.Remove(front)
		}
	}

	e := c.list.PushBack(key)
	c.cacheMap[key] = e

	return true
}

func (c *LRUTxCache) Remove(tx types.Tx) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	key := tx.Key()
	e := c.cacheMap[key]
	delete(c.cacheMap, key)
	delete(c.rejections, key)
	delete(c.committed, key)

	if e != nil {
		c.list.Remove(e)
	}
}

func (c *LRUTxCache) Has(tx types.Tx) bool {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	_, ok := c.cacheMap[tx.Key()]
	return ok
}

func (c *LRUTxCache) SetRejection(tx types.Tx, res *abci.ResponseCheckTx) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	key := tx.Key()
	if _, ok := c.cacheMap[key]; !ok {
		return
	}
	if _, ok := c.committed[key]; ok {
		return
	}
	if len(res.Codespace)+len(res.Info)+len(res.Data) > maxCachedRejectionBytes {
		delete(c.rejections, key)
		return
	}

	c.rejections[key] = &abci.ResponseCheckTx{
		Code:      res.Code,
		Codespace: res.Codespace,
		Info:      res.Info,
		Data:      bytes.Clone(res.Data),
	}
}

func (c *LRUTxCache) MarkCommitted(tx types.Tx) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	key := tx.Key()
	if _, ok := c.cacheMap[key]; !ok {
		return
	}
	delete(c.rejections, key)
	c.committed[key] = struct{}{}
}

func (c *LRUTxCache) Rejection(tx types.Tx) *abci.ResponseCheckTx {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	res, ok := c.rejections[tx.Key()]
	if !ok {
		return nil
	}

	return &abci.ResponseCheckTx{
		Code:      res.Code,
		Codespace: res.Codespace,
		Info:      res.Info,
		Data:      bytes.Clone(res.Data),
	}
}

// NopTxCache defines a no-op raw transaction cache.
type NopTxCache struct{}

var _ TxCache = (*NopTxCache)(nil)

func (NopTxCache) Reset()             {}
func (NopTxCache) Push(types.Tx) bool { return true }
func (NopTxCache) Remove(types.Tx)    {}
func (NopTxCache) Has(types.Tx) bool  { return false }

func (NopTxCache) SetRejection(types.Tx, *abci.ResponseCheckTx) {}
func (NopTxCache) MarkCommitted(types.Tx)                       {}
func (NopTxCache) Rejection(types.Tx) *abci.ResponseCheckTx     { return nil }
