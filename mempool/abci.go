package mempool

import (
	"context"
	"fmt"
	"sync"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

// ABCI provides an ABCI integration with a mempool store. The ABCI
// client enforces the tendermint guarantees.
type ABCI struct {
	cfg    *config.MempoolConfig
	logger log.Logger

	txsAvailable         chan struct{}
	notifiedTxsAvailable bool

	// cache defines a fixed-size cache of already seen transactions as this
	// reduces pressure on the proxyApp.
	cache txCacher

	mtx     sync.RWMutex
	pool    Pool
	appConn proxy.AppConnMempool
}

// NewABCI constructs a new ABCI type.
func NewABCI(logger log.Logger, cfg *config.MempoolConfig, appClient proxy.AppConnMempool, pool Pool) *ABCI {
	out := ABCI{
		cfg:     cfg,
		logger:  logger,
		cache:   nopTxCache{},
		pool:    pool,
		appConn: appClient,
	}

	if size := cfg.CacheSize; size > 0 {
		out.cache = newMapTxCache(size)
	}

	out.appConn.SetResponseCallback(out.globalCb)

	return &out
}

// AfterBlockFinality iterates over all the transactions provided by the block
// producer, removes them from the cache (if applicable), and removes the
// transactions from the main transaction store and associated indexes.
// If there are transactions remaining in the mempool, we initiate a
// re-CheckTx for them (if applicable), otherwise, we notify the caller more
// transactions are available.
//
// NOTE:
// - The caller must explicitly call PrepBlockFinality, which locks the client
//	 and flushes the appconn to enforce we sync state correctly with the app
//	 on Update. Failure to do so will result in an error.
func (a *ABCI) AfterBlockFinality(
	ctx context.Context,
	block *types.Block,
	txResults []*abci.ResponseDeliverTx,
	newPreFn PreCheckFunc,
	newPostFn PostCheckFunc,
) error {
	if a.mtx.TryLock() {
		a.mtx.Unlock()
		// this TryLock call enforces to Lock the ABCI client before executing
		// the AfterBlockFinality method.
		return fmt.Errorf("caller failed to secure write lock on update; aborting to avoid corrupting application state")
	}

	a.notifiedTxsAvailable = false

	for i := range block.Txs {
		cacheOp := a.addToCache
		if txResults[i].Code != abci.CodeTypeOK {
			cacheOp = a.removeFromCache
		}
		cacheOp(block.Txs[i])
	}

	opRes, err := a.pool.OnBlockFinality(ctx, block, newPreFn, newPostFn)
	if err != nil {
		return err
	}

	return a.applyOpResult(ctx, opRes)
}

// CheckTx executes the ABCI CheckTx method for a given transaction.
// It acquires a read-lock and attempts to execute the application's
// CheckTx ABCI method synchronously. We return an error if any of
// the following happen:
//
// - The CheckTx execution fails.
// - The transaction already exists in the cache and we've already received the
//   transaction from the peer. Otherwise, if it solely exists in the cache, we
//   return nil.
//   TODO(berg): the above is not accurate, we do not error on that case. It makes
//				 makes sense to include the peer state here in ABCI type and not the
//				 mempool store itself.
// - The transaction size exceeds the maximum transaction size as defined by the
//   configuration provided to the mempool.
// - The transaction fails Pre-Check (if it is defined).
// - The proxyAppConn fails, e.g. the buffer is full.
// NOTE:
// - The applications' CheckTx implementation may panic.
func (a *ABCI) CheckTx(ctx context.Context, tx types.Tx, callback func(*abci.Response), txInfo TxInfo) error {
	a.mtx.RLock()
	defer a.mtx.RUnlock()

	if txSize, maxTXBytes := len(tx), a.cfg.MaxTxBytes; txSize > maxTXBytes {
		return ErrTxTooLarge{
			Max:    maxTXBytes,
			Actual: txSize,
		}
	}

	if err := a.pool.CheckTxPrep(ctx, tx); err != nil {
		return err
	}

	if err := a.appConn.Error(); err != nil {
		return err
	}

	// We add the transaction to the mempool's cache and if the
	// transaction is already present in the cache, i.e. false is returned, then we
	// check if we've seen this transaction and error if we have.
	if !a.cache.Push(tx) {
		// TODO(berg): the below is pulled directly from ClistMempool. I don't understand what
		// 			   it is doing? None of the returned types are being used? The comment
		//			   feels before the if block feels misleading. We don't check if we've
		//			   seen the tx before and error, we just call it and if it gets added
		//			   to the txstore, so be it :shruggo:
		// txmp.txStore.GetOrSetPeerByTxHash(txHash, info.SenderID)
		return ErrTxInCache
	}

	reqRes := a.appConn.CheckTxAsync(abci.RequestCheckTx{Tx: tx})
	reqRes.SetCallback(func(res *abci.Response) {
		r, ok := res.Value.(*abci.Response_CheckTx)
		if !ok {
			// TODO(berg): perhaps adds some logging here?
			return
		}

		// TODO(berg): we don't support ctx propogration in this version of abci, but will
		// in future versions. Update this when that becomes available.
		ctx := context.TODO()
		checkTx := r.CheckTx

		opRes := a.pool.CheckTxCallback(ctx, tx, checkTx, txInfo)

		err := a.applyOpResult(ctx, opRes)
		if err != nil {
			a.logger.Error("failed to apply operation results", "err", err)
			return
		}

		if callback != nil {
			callback(res)
		}
	})

	return nil
}

// EnableTxsAvailable enables the mempool to trigger events when transactions
// are available on a block by block basis.
func (a *ABCI) EnableTxsAvailable() {
	a.mtx.Lock()
	defer a.mtx.Unlock()

	a.txsAvailable = make(chan struct{}, 1)
}

// Flush clears the tx cache and then calls Flush on the underlying pool.
func (a *ABCI) Flush(ctx context.Context) error {
	a.mtx.Lock()
	defer a.mtx.Unlock()
	a.cache.Reset()
	return a.pool.Flush(ctx)
}

func (a *ABCI) HydrateBlockData(ctx context.Context, bl *types.Block) (types.Data, error) {
	return a.pool.HydrateBlockData(ctx, bl)
}

// PoolMeta returns the metadata for the underlying pool implementation.
func (a *ABCI) PoolMeta() PoolMeta {
	return a.pool.Meta()
}

// PrepBlockFinality prepares the mempool for an upcoming block to be finalized. During
// the execution of the new block, we want to make sure we are not running any additional
// CheckTX calls to the application as well as flush any active connections underway.
func (a *ABCI) PrepBlockFinality(_ context.Context) (func(), error) {
	a.mtx.Lock()
	finishFn := func() { a.mtx.Unlock() }

	err := a.appConn.FlushSync()
	if err != nil {
		finishFn() // make sure we unlock before returning
		return func() {}, err
	}

	return finishFn, nil
}

// Reap calls the underlying pool's Reap method with the given options.
func (a *ABCI) Reap(ctx context.Context, opts ...ReapOptFn) (ReapResults, error) {
	opt := CoalesceReapOpts(opts...)
	res, err := a.pool.Reap(ctx, opt)
	if err != nil {
		return ReapResults{}, err
	}

	if opt.Verify {
		return a.verifyReaper(ctx, res)
	}

	return res, nil
}

func (a *ABCI) verifyReaper(ctx context.Context, res ReapResults) (ReapResults, error) {
	// TODO(berg): wire this up, for now just a no op, all txs are good...
	return res, nil
}

// Remove removes txs from the cache and underlying pool.
func (a *ABCI) Remove(ctx context.Context, opts ...RemOptFn) error {
	opt := CoalesceRemOptFns(opts...)
	opRes, err := a.pool.Remove(ctx, opt)
	if err != nil {
		return err
	}

	return a.applyOpResult(ctx, opRes)
}

// TxsAvailable returns a channel which sends one value for every height, and only
// when transactions are available in the mempool. It is safe for concurrent use.
func (a *ABCI) TxsAvailable() <-chan struct{} {
	return a.txsAvailable
}

func (a *ABCI) globalCb(req *abci.Request, res *abci.Response) {
	r, ok := res.Value.(*abci.Response_CheckTx)
	if !ok {
		return
	}

	tx := req.GetCheckTx().Tx
	opRes, err := a.pool.GlobalCheck(tx, r.CheckTx)
	if err != nil {
		a.logger.Error("failed pool global callback", "err", err)
	}
	// using context.TODO() here as we're not using ctx within abci in this
	// branch. Once that lands we can propogate as needed.
	err = a.applyOpResult(context.TODO(), opRes)
	if err != nil {
		a.logger.Error("failed to apply operation results", "err", err)
	}
}

func (a *ABCI) applyOpResult(ctx context.Context, o OpResult) error {
	for _, tx := range o.RemovedTxs {
		a.removeFromCache(tx)
	}

	switch o.Status {
	case StatusTxsAvailable:
		a.notifyTxsAvailable()
	case StatusRecheckReady:
		opres, err := a.pool.Recheck(ctx, a.appConn)
		if err != nil {
			a.logger.Error("failed pool recheck", "err", err)
		}
		return a.applyOpResult(ctx, opres)
	}
	return nil
}

func (a *ABCI) addToCache(tx types.Tx) {
	a.cache.Push(tx)
}

func (a *ABCI) removeFromCache(tx types.Tx) {
	if a.cfg.KeepInvalidTxsInCache {
		return
	}
	a.cache.Remove(tx)
}

func (a *ABCI) notifyTxsAvailable() {
	if a.txsAvailable == nil || a.notifiedTxsAvailable {
		return
	}

	// channel cap is 1, so this will send once
	a.notifiedTxsAvailable = true
	select {
	case a.txsAvailable <- struct{}{}:
	default:
	}
}
