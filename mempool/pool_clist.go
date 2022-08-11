package mempool

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	abci "github.com/tendermint/tendermint/abci/types"
	cfg "github.com/tendermint/tendermint/config"
	auto "github.com/tendermint/tendermint/libs/autofile"
	"github.com/tendermint/tendermint/libs/clist"
	"github.com/tendermint/tendermint/libs/log"
	tmmath "github.com/tendermint/tendermint/libs/math"
	tmos "github.com/tendermint/tendermint/libs/os"
	tmsync "github.com/tendermint/tendermint/libs/sync"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

var newline = []byte("\n")

// --------------------------------------------------------------------------------

// CListMempoolOption sets an optional parameter on the mempool.
type CListMempoolOption func(cList *PoolCList)

// WithPreCheck sets a filter for the mempool to reject a tx if f(tx) returns
// false. This is ran before CheckTx. Only applies to the first created block.
// After that, Update overwrites the existing value.
func WithPreCheck(f PreCheckFunc) CListMempoolOption {
	return func(mem *PoolCList) { mem.preCheck = f }
}

// WithPostCheck sets a filter for the mempool to reject a tx if f(tx) returns
// false. This is ran after CheckTx. Only applies to the first created block.
// After that, Update overwrites the existing value.
func WithPostCheck(f PostCheckFunc) CListMempoolOption {
	return func(mem *PoolCList) { mem.postCheck = f }
}

// WithMetrics sets the metrics.
func WithMetrics(metrics *Metrics) CListMempoolOption {
	return func(mem *PoolCList) { mem.metrics = metrics }
}

// --------------------------------------------------------------------------------

// PoolCList is an ordered in-memory pool for transactions before they are
// proposed in a consensus round. Transaction validity is checked using the
// CheckTx abci message before the transaction is added to the pool. The
// mempool uses a concurrent list structure for storing transactions that can
// be efficiently accessed by multiple concurrent readers.
type PoolCList struct {
	// Atomic integers
	height   int64 // the last block Update()'d to
	txsBytes int64 // total size of mempool, in bytes

	config *cfg.MempoolConfig

	// Exclusive mutex for Update method to prevent concurrent execution of
	// CheckTx or reapMaxBytesMaxGas(reapMaxTxs) methods.
	updateMtx tmsync.RWMutex
	preCheck  PreCheckFunc
	postCheck PostCheckFunc

	wal *auto.AutoFile // a log of mempool txs
	txs *clist.CList   // concurrent linked-list of good txs

	// Track whether we're rechecking txs.
	// These are not protected by a mutex and are expected to be mutated in
	// serial (ie. by abci responses which are called in serial).
	recheckCursor *clist.CElement // next expected response
	recheckEnd    *clist.CElement // re-checking stops here

	// Map for quick access to txs to record sender in CheckTx.
	// txsMap: txKey -> CElement
	txsMap sync.Map

	logger log.Logger

	metrics *Metrics
}

var _ Pool = (*PoolCList)(nil)

// NewPoolCList returns a new mempool with the given configuration and connection to an application.
func NewPoolCList(config *cfg.MempoolConfig, height int64, options ...CListMempoolOption) *PoolCList {
	mempool := PoolCList{
		config:        config,
		txs:           clist.New(),
		height:        height,
		recheckCursor: nil,
		recheckEnd:    nil,
		logger:        log.NewNopLogger(),
		metrics:       NopMetrics(),
	}

	for _, option := range options {
		option(&mempool)
	}
	return &mempool
}

func (mem *PoolCList) CheckTxCallback(ctx context.Context, tx types.Tx, res *abci.ResponseCheckTx, info TxInfo) OpResult {
	if mem.recheckCursor != nil {
		// this should never happen
		panic("recheck cursor is not nil in reqResCb")
	}

	opRes := mem.resCbFirstTime(tx, info.SenderID, info.SenderP2PID, res)

	// update metrics
	mem.metrics.Size.Set(float64(mem.size()))

	return opRes
}

func (mem *PoolCList) Flush(ctx context.Context) error {
	mem.updateMtx.RLock()
	defer mem.updateMtx.RUnlock()

	_ = atomic.SwapInt64(&mem.txsBytes, 0)

	for e := mem.txs.Front(); e != nil; e = e.Next() {
		mem.txs.Remove(e)
		e.DetachPrev()
	}

	mem.txsMap.Range(func(key, _ interface{}) bool {
		mem.txsMap.Delete(key)
		return true
	})

	return nil
}

func (mem *PoolCList) GlobalCheck(tx types.Tx, res *abci.ResponseCheckTx) (OpResult, error) {
	if mem.recheckCursor == nil {
		return OpResult{}, nil
	}

	mem.metrics.RecheckTimes.Add(1)

	memTx := mem.recheckCursor.Value.(*mempoolTx)
	if !bytes.Equal(tx, memTx.tx) {
		panic(fmt.Sprintf(
			"Unexpected tx response from proxy during recheck\nExpected %X, got %X",
			memTx.tx,
			tx))
	}
	var postCheckErr error
	if mem.postCheck != nil {
		postCheckErr = mem.postCheck(tx, res)
	}

	var opRes OpResult
	if (res.Code == abci.CodeTypeOK) && postCheckErr == nil {
		// Good, nothing to do.
	} else {
		// Tx became invalidated due to newly committed block.
		mem.logger.Debug("tx is no longer valid", "tx", txID(tx), "res", res, "err", postCheckErr)
		// NOTE: we remove tx from the cache because it might be good later
		mem.removeTx(tx, mem.recheckCursor)
		opRes.RemovedTxs = append(opRes.RemovedTxs, tx)
	}

	if mem.recheckCursor == mem.recheckEnd {
		mem.recheckCursor = nil
	} else {
		mem.recheckCursor = mem.recheckCursor.Next()
	}

	if mem.recheckCursor == nil {
		// Done!
		mem.logger.Debug("done rechecking txs")

		// incase the recheck removed all txs
		opRes.Status = mem.getOpResult().Status
	}

	// update metrics
	mem.metrics.Size.Set(float64(mem.size()))

	return opRes, nil
}

func (mem *PoolCList) HydratedBlockData(ctx context.Context, block *types.Block) (types.Data, error) {
	return block.Data, nil
}

func (mem *PoolCList) Meta() PoolMeta {
	return PoolMeta{
		Type:       "clist",
		Size:       mem.size(),
		TotalBytes: mem.totalBytes(),
	}
}

func (mem *PoolCList) OnBlockFinality(
	ctx context.Context,
	block *types.Block,
	newPreFn PreCheckFunc,
	newPostFn PostCheckFunc,
) (OpResult, error) {
	height, txs := block.Height, block.Txs
	mem.height = height

	if newPreFn != nil {
		mem.preCheck = newPreFn
	}
	if newPostFn != nil {
		mem.postCheck = newPostFn
	}

	for _, tx := range txs {
		// Remove committed tx from the mempool.
		//
		// Note an evil proposer can drop valid txs!
		// Mempool before:
		//   100 -> 101 -> 102
		// Block, proposed by an evil proposer:
		//   101 -> 102
		// Mempool after:
		//   100
		// https://github.com/tendermint/tendermint/issues/3322.
		if e, ok := mem.txsMap.Load(TxKey(tx)); ok {
			mem.removeTx(tx, e.(*clist.CElement))
		}
	}

	opRes := mem.getOpResult()
	if mem.size() > 0 && mem.config.Recheck {
		mem.logger.Debug("recheck txs", "numtxs", mem.size(), "height", height)
		opRes.Status = StatusRecheckReady
		// At this point, mem.txs are being rechecked.
		// mem.recheckCursor re-scans mem.txs and possibly removes some txs.
		// Before mem.Reap(), we should wait for mem.recheckCursor to be nil.
	}

	return opRes, nil
}

func (mem *PoolCList) CheckTxPrep(ctx context.Context, tx types.Tx) error {
	txSize := len(tx)
	if err := mem.isFull(txSize); err != nil {
		return err
	}

	if mem.preCheck != nil {
		if err := mem.preCheck(tx); err != nil {
			return ErrPreCheck{Reason: err}
		}
	}

	// NOTE: writing to the WAL and calling proxy must be done before adding tx
	// to the cache. otherwise, if either of them fails, next time CheckTx is
	// called with tx, ErrTxInCache will be returned without tx being checked at
	// all even once.
	if mem.wal != nil {
		// TODO: Notify administrators when WAL fails
		_, err := mem.wal.Write(append([]byte(tx), newline...))
		if err != nil {
			return fmt.Errorf("wal.Write: %w", err)
		}
	}

	return nil
}

func (mem *PoolCList) Reap(ctx context.Context, opt ReapOption) (types.Data, error) {
	if opt.NumTxs > -1 && (opt.GasLimit > -1 || opt.BlockSizeLimit > -1) {
		return types.Data{}, fmt.Errorf("reaping by num txs and one of gas limit or block size limit is not supported")
	}

	var txs types.Txs
	switch {
	case opt.NumTxs == -1 && opt.GasLimit == -1 && opt.BlockSizeLimit == -1:
		txs = mem.reapMaxTxs(-1)
	case opt.NumTxs > -1:
		txs = mem.reapMaxTxs(opt.NumTxs)
	default:
		txs = mem.reapMaxBytesMaxGas(opt.BlockSizeLimit, opt.GasLimit)
	}

	return types.Data{Txs: txs}, nil
}

func (mem *PoolCList) Recheck(ctx context.Context, appConn proxy.AppConnMempool) (OpResult, error) {
	if mem.size() == 0 {
		return OpResult{}, nil
	}

	mem.recheckCursor = mem.txs.Front()
	mem.recheckEnd = mem.txs.Back()

	// Push txs to proxyAppConn
	// NOTE: globalCb may be called concurrently.
	for e := mem.txs.Front(); e != nil; e = e.Next() {
		memTx := e.Value.(*mempoolTx)
		appConn.CheckTxAsync(abci.RequestCheckTx{
			Tx:   memTx.tx,
			Type: abci.CheckTxType_Recheck,
		})
	}

	appConn.FlushAsync()

	return mem.getOpResult(), nil
}

func (mem *PoolCList) Remove(ctx context.Context, opt RemOption) (OpResult, error) {
	var (
		opRes    OpResult
		errParts []string
	)
	for _, txKey := range opt.TxKeys {
		tx, isRemoved := mem.removeTxByKey(txKey)
		if !isRemoved {
			continue
		}
		opRes.RemovedTxs = append(opRes.RemovedTxs, tx)
	}

	var err error
	if len(errParts) > 0 {
		errPartMsg := strings.Join(errParts, "\n\t*")
		err = fmt.Errorf("failed to remove keys with err(s): \n\t*%s", errPartMsg)
	}

	return opRes, err
}

// SetLogger sets the Logger.
func (mem *PoolCList) SetLogger(l log.Logger) {
	mem.logger = l
}

func (mem *PoolCList) InitWAL() error {
	var (
		walDir  = mem.config.WalDir()
		walFile = walDir + "/wal"
	)

	const perm = 0700
	if err := tmos.EnsureDir(walDir, perm); err != nil {
		return err
	}

	af, err := auto.OpenAutoFile(walFile)
	if err != nil {
		return fmt.Errorf("can't open autofile %s: %w", walFile, err)
	}

	mem.wal = af
	return nil
}

func (mem *PoolCList) CloseWAL() {
	if err := mem.wal.Close(); err != nil {
		mem.logger.Error("Error closing WAL", "err", err)
	}
	mem.wal = nil
}

// Safe for concurrent use by multiple goroutines.
func (mem *PoolCList) size() int {
	return mem.txs.Len()
}

// Safe for concurrent use by multiple goroutines.
func (mem *PoolCList) totalBytes() int64 {
	return atomic.LoadInt64(&mem.txsBytes)
}

// TxsFront returns the first transaction in the ordered list for peer
// goroutines to call .NextWait() on.
// FIXME: leaking implementation details!
//
// Safe for concurrent use by multiple goroutines.
func (mem *PoolCList) TxsFront() *clist.CElement {
	return mem.txs.Front()
}

// TxsWaitChan returns a channel to wait on transactions. It will be closed
// once the mempool is not empty (ie. the internal `mem.txs` has at least one
// element)
//
// Safe for concurrent use by multiple goroutines.
func (mem *PoolCList) TxsWaitChan() <-chan struct{} {
	return mem.txs.WaitChan()
}

// Called from:
//  - resCbFirstTime (lock not held) if tx is valid
func (mem *PoolCList) addTx(memTx *mempoolTx) {
	e := mem.txs.PushBack(memTx)
	mem.txsMap.Store(TxKey(memTx.tx), e)
	atomic.AddInt64(&mem.txsBytes, int64(len(memTx.tx)))
	mem.metrics.TxSizeBytes.Observe(float64(len(memTx.tx)))
}

// Called from:
//  - Update (lock held) if tx was committed
// 	- resCbRecheck (lock not held) if tx was invalidated
func (mem *PoolCList) removeTx(tx types.Tx, elem *clist.CElement) {
	mem.txs.Remove(elem)
	elem.DetachPrev()
	mem.txsMap.Delete(TxKey(tx))
	atomic.AddInt64(&mem.txsBytes, int64(-len(tx)))
}

func (mem *PoolCList) removeTxByKey(txKey types.TxKey) (types.Tx, bool) {
	e, ok := mem.txsMap.Load(txKey)
	if !ok {
		return nil, false
	}

	memTx := e.(*clist.CElement).Value.(*mempoolTx)
	if memTx != nil {
		mem.removeTx(memTx.tx, e.(*clist.CElement))
	}

	return memTx.tx, true
}

func (mem *PoolCList) isFull(txSize int) error {
	var (
		memSize  = mem.size()
		txsBytes = mem.totalBytes()
	)

	if memSize >= mem.config.Size || int64(txSize)+txsBytes > mem.config.MaxTxsBytes {
		return ErrMempoolIsFull{
			NumTxs: memSize, MaxTxs: mem.config.Size,
			TxsBytes: txsBytes, MaxTxsBytes: mem.config.MaxTxsBytes,
		}
	}

	return nil
}

// callback, which is called after the app checked the tx for the first time.
//
// The case where the app checks the tx for the second and subsequent times is
// handled by the resCbRecheck callback.
func (mem *PoolCList) resCbFirstTime(
	tx []byte,
	peerID uint16,
	peerP2PID p2p.ID,
	r *abci.ResponseCheckTx,
) OpResult {
	var postCheckErr error
	if mem.postCheck != nil {
		postCheckErr = mem.postCheck(tx, r)
	}

	if r.Code != abci.CodeTypeOK || postCheckErr != nil {
		// ignore bad transaction
		mem.logger.Debug("rejected bad transaction",
			"tx", txID(tx), "peerID", peerP2PID, "res", r, "err", postCheckErr)
		mem.metrics.FailedTxs.Add(1)

		return OpResult{RemovedTxs: types.Txs{tx}}
	}

	// Check mempool isn't full again to reduce the chance of exceeding the
	// limits.
	if err := mem.isFull(len(tx)); err != nil {
		mem.logger.Error(err.Error())
		return OpResult{RemovedTxs: types.Txs{tx}}
	}

	memTx := &mempoolTx{
		height:    mem.height,
		gasWanted: r.GasWanted,
		tx:        tx,
	}
	memTx.senders.Store(peerID, true)
	mem.addTx(memTx)
	mem.logger.Debug("added good transaction",
		"tx", txID(tx),
		"res", r,
		"height", memTx.height,
		"total", mem.size(),
	)

	return mem.getOpResult()
}

func (mem *PoolCList) getOpResult() OpResult {
	var opRes OpResult
	if mem.size() > 0 {
		opRes.Status = StatusTxsAvailable
	}
	return opRes
}

// Safe for concurrent use by multiple goroutines.
func (mem *PoolCList) reapMaxBytesMaxGas(maxBytes, maxGas int64) types.Txs {
	mem.updateMtx.RLock()
	defer mem.updateMtx.RUnlock()

	var totalGas int64

	// TODO: we will get a performance boost if we have a good estimate of avg
	// size per tx, and set the initial capacity based off of that.
	// txs := make([]types.Tx, 0, tmmath.MinInt(mem.txs.Len(), max/mem.avgTxSize))
	txs := make([]types.Tx, 0, mem.txs.Len())
	for e := mem.txs.Front(); e != nil; e = e.Next() {
		memTx := e.Value.(*mempoolTx)

		dataSize := types.ComputeProtoSizeForTxs(append(txs, memTx.tx))

		// Check total size requirement
		if maxBytes > -1 && dataSize > maxBytes {
			return txs
		}
		// Check total gas requirement.
		// If maxGas is negative, skip this check.
		// Since newTotalGas < masGas, which
		// must be non-negative, it follows that this won't overflow.
		newTotalGas := totalGas + memTx.gasWanted
		if maxGas > -1 && newTotalGas > maxGas {
			return txs
		}
		totalGas = newTotalGas
		txs = append(txs, memTx.tx)
	}
	return txs
}

// Safe for concurrent use by multiple goroutines.
func (mem *PoolCList) reapMaxTxs(max int) types.Txs {
	mem.updateMtx.RLock()
	defer mem.updateMtx.RUnlock()

	if max < 0 {
		max = mem.txs.Len()
	}

	txs := make([]types.Tx, 0, tmmath.MinInt(mem.txs.Len(), max))
	for e := mem.txs.Front(); e != nil && len(txs) <= max; e = e.Next() {
		memTx := e.Value.(*mempoolTx)
		txs = append(txs, memTx.tx)
	}
	return txs
}

// --------------------------------------------------------------------------------

type txCacher interface {
	Reset()
	Push(tx types.Tx) bool
	Remove(tx types.Tx)
}

// mapTxCache maintains a LRU cache of transactions. This only stores the hash
// of the tx, due to memory concerns.
type mapTxCache struct {
	mtx      tmsync.Mutex
	size     int
	cacheMap map[types.TxKey]*list.Element
	list     *list.List
}

var _ txCacher = (*mapTxCache)(nil)

// newMapTxCache returns a new mapTxCache.
func newMapTxCache(cacheSize int) *mapTxCache {
	return &mapTxCache{
		size:     cacheSize,
		cacheMap: make(map[types.TxKey]*list.Element, cacheSize),
		list:     list.New(),
	}
}

// Reset resets the cache to an empty state.
func (cache *mapTxCache) Reset() {
	cache.mtx.Lock()
	cache.cacheMap = make(map[types.TxKey]*list.Element, cache.size)
	cache.list.Init()
	cache.mtx.Unlock()
}

// Push adds the given tx to the cache and returns true. It returns
// false if tx is already in the cache.
func (cache *mapTxCache) Push(tx types.Tx) bool {
	cache.mtx.Lock()
	defer cache.mtx.Unlock()

	// Use the tx hash in the cache
	txHash := TxKey(tx)
	if moved, exists := cache.cacheMap[txHash]; exists {
		cache.list.MoveToBack(moved)
		return false
	}

	if cache.list.Len() >= cache.size {
		popped := cache.list.Front()
		if popped != nil {
			poppedTxHash := popped.Value.(types.TxKey)
			delete(cache.cacheMap, poppedTxHash)
			cache.list.Remove(popped)
		}
	}
	e := cache.list.PushBack(txHash)
	cache.cacheMap[txHash] = e
	return true
}

// Remove removes the given tx from the cache.
func (cache *mapTxCache) Remove(tx types.Tx) {
	cache.mtx.Lock()
	txHash := TxKey(tx)
	popped := cache.cacheMap[txHash]
	delete(cache.cacheMap, txHash)
	if popped != nil {
		cache.list.Remove(popped)
	}

	cache.mtx.Unlock()
}

// mempoolTx is a transaction that successfully ran
type mempoolTx struct {
	height    int64    // height that this tx had been validated in
	gasWanted int64    // amount of gas this tx states it will require
	tx        types.Tx //

	// ids of peers who've sent us this tx (as a map for quick lookups).
	// senders: PeerID -> bool
	senders sync.Map
}

// Height returns the height for this transaction
func (memTx *mempoolTx) Height() int64 {
	return atomic.LoadInt64(&memTx.height)
}

// --------------------------------------------------------------------------------

type nopTxCache struct{}

var _ txCacher = (*nopTxCache)(nil)

func (nopTxCache) Reset()             {}
func (nopTxCache) Push(types.Tx) bool { return true }
func (nopTxCache) Remove(types.Tx)    {}

// --------------------------------------------------------------------------------

// TxKey is the fixed length array hash used as the key in maps.
func TxKey(tx types.Tx) types.TxKey {
	return sha256.Sum256(tx)
}

// txID is a hash of the Tx.
func txID(tx []byte) []byte {
	return types.Tx(tx).Hash()
}
