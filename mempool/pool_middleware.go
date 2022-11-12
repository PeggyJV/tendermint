package mempool

import (
	"context"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/observe"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

type PoolMiddleware func(pool Pool) Pool

type middlewareStats struct {
	logger log.Logger
	next   Pool

	checkTxCallbackRED observe.RED
	checkTxPrepRED     observe.RED
	flushRED           observe.RED // this sounds bad.... >_<
	globalCheckRED     observe.RED
	hydrateBlockRED    observe.RED
	onBlockFinalityRED observe.RED
	reapRED            observe.RED
	recheckRED         observe.RED
	removeRED          observe.RED
}

func ObservePool(logger log.Logger, namespace string, labelsAndVals ...string) PoolMiddleware {
	labelsAndVals = append(labelsAndVals, "component", "pool_middleware")
	newRED := func(opName string) observe.RED {
		return observe.NewRED(namespace, MetricsSubsystem, opName, labelsAndVals...)
	}
	return func(pool Pool) Pool {
		return &middlewareStats{
			logger: logger.With("component", "pool_observer"),
			next:   pool,

			checkTxCallbackRED: newRED("check_tx_callback"),
			checkTxPrepRED:     newRED("check_tx_prep"),
			flushRED:           newRED("flush_pool"),
			globalCheckRED:     newRED("global_check"),
			hydrateBlockRED:    newRED("hydrate_block_data"),
			onBlockFinalityRED: newRED("on_block_finality"),
			reapRED:            newRED("reap_pool"),
			recheckRED:         newRED("recheck_pool"),
			removeRED:          newRED("remove_pool"),
		}
	}
}

func (m *middlewareStats) CheckTxCallback(ctx context.Context, tx types.Tx, res *abci.ResponseCheckTx, txInfo TxInfo) OpResult {
	ctx = withObservations(ctx)
	defer m.observe(ctx, m.checkTxCallbackRED, "CheckTxCallback")(nil)
	return m.next.CheckTxCallback(ctx, tx, res, txInfo)
}

func (m *middlewareStats) CheckTxPrep(ctx context.Context, tx types.Tx) error {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.checkTxPrepRED, "CheckTxPrep")
	return record(m.next.CheckTxPrep(ctx, tx))
}

func (m *middlewareStats) Flush(ctx context.Context) error {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.flushRED, "Flush")
	return record(m.next.Flush(ctx))
}

func (m *middlewareStats) GlobalCheck(tx types.Tx, res *abci.ResponseCheckTx) (OpResult, error) {
	record := m.globalCheckRED.Incr()
	opRes, err := m.next.GlobalCheck(tx, res)
	record(err)
	return opRes, err
}

func (m *middlewareStats) HydrateBlockData(ctx context.Context, block *types.Block) (types.Data, error) {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.hydrateBlockRED, "HydrateBlockData",
		"block_size", block.Size(),
		"height", block.Height,
		"num_colls", block.Collections.Count(),
		"num_txs", len(block.Txs),
	)
	data, err := m.next.HydrateBlockData(ctx, block)
	return data, record(err)
}

func (m *middlewareStats) Meta() PoolMeta {
	return m.next.Meta()
}

func (m *middlewareStats) OnBlockFinality(ctx context.Context, block *types.Block, newPreFn PreCheckFunc, newPostFn PostCheckFunc) (OpResult, error) {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.hydrateBlockRED, "OnBlockFinality",
		"block_size", block.Size(),
		"height", block.Height,
		"num_colls", block.Collections.Count(),
		"num_txs", len(block.Txs),
	)
	opRes, err := m.next.OnBlockFinality(ctx, block, newPreFn, newPostFn)
	return opRes, record(err)
}

func (m *middlewareStats) Reap(ctx context.Context, opts ReapOption) (ReapResults, error) {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.reapRED, "Reap",
		"max_txs", opts.NumTxs,
		"max_size", opts.BlockSizeLimit,
		"max_gas", opts.GasLimit,
		"verified", opts.Verify,
	)
	res, err := m.next.Reap(ctx, opts)
	return res, record(err)
}

func (m *middlewareStats) Recheck(ctx context.Context, appConn proxy.AppConnMempool) (OpResult, error) {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.recheckRED, "Recheck")
	opRes, err := m.next.Recheck(ctx, appConn)
	return opRes, record(err)
}

func (m *middlewareStats) Remove(ctx context.Context, opts RemOption) (OpResult, error) {
	ctx = withObservations(ctx)
	record := m.observe(ctx, m.removeRED, "Remove",
		"num_collections", opts.Collections.Count(),
		"num_tx_keys", len(opts.TxKeys),
	)
	opRes, err := m.next.Remove(ctx, opts)
	return opRes, record(err)
}

func (m *middlewareStats) observe(ctx context.Context, red observe.RED, op string, kvPairs ...any) func(err error) error {
	record := red.Incr()
	return func(err error) error {
		record(err)
		m.observeOp(ctx, op, kvPairs...)
		return err
	}
}

func (m *middlewareStats) observeOp(ctx context.Context, op string, kvPairs ...any) {
	kvPairs = append(kvPairs,
		"op", op,
		"took", observe.Since(ctx).String(),
		"trace_id", observe.TraceID(ctx),
	)
	m.logger.Debug("observed "+op, kvPairs...)
}

func withObservations(ctx context.Context) context.Context {
	ctx = observe.WithTraceID(ctx)
	return observe.WithStartTime(ctx)
}
