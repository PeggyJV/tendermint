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
}

func ObservePool(logger log.Logger) PoolMiddleware {
	return func(pool Pool) Pool {
		return &middlewareStats{
			logger: logger,
			next:   pool,
		}
	}
}

func (m *middlewareStats) CheckTxCallback(ctx context.Context, tx types.Tx, res *abci.ResponseCheckTx, txInfo TxInfo) OpResult {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "CheckTxCallback")
	return m.next.CheckTxCallback(ctx, tx, res, txInfo)
}

func (m *middlewareStats) CheckTxPrep(ctx context.Context, tx types.Tx) error {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "CheckTxPrep")
	return m.next.CheckTxPrep(ctx, tx)
}

func (m *middlewareStats) Flush(ctx context.Context) error {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "Flush")
	return m.next.Flush(ctx)
}

func (m *middlewareStats) GlobalCheck(tx types.Tx, res *abci.ResponseCheckTx) (OpResult, error) {
	return m.next.GlobalCheck(tx, res)
}

func (m *middlewareStats) HydrateBlockData(ctx context.Context, block *types.Block) (types.Data, error) {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "HydrateBlockData",
		"block_size", block.Size(),
		"height", block.Height,
		"num_colls", block.Collections.Count(),
		"num_txs", len(block.Txs),
	)
	return m.next.HydrateBlockData(ctx, block)
}

func (m *middlewareStats) Meta() PoolMeta {
	return m.next.Meta()
}

func (m *middlewareStats) OnBlockFinality(ctx context.Context, block *types.Block, newPreFn PreCheckFunc, newPostFn PostCheckFunc) (OpResult, error) {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "OnBlockFinality",
		"block_size", block.Size(),
		"height", block.Height,
		"num_colls", block.Collections.Count(),
		"num_txs", len(block.Txs),
	)
	return m.next.OnBlockFinality(ctx, block, newPreFn, newPostFn)
}

func (m *middlewareStats) Reap(ctx context.Context, opts ReapOption) (ReapResults, error) {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "Reap",
		"max_txs", opts.NumTxs,
		"max_size", opts.BlockSizeLimit,
		"max_gas", opts.GasLimit,
		"verified", opts.Verify,
	)
	return m.next.Reap(ctx, opts)
}

func (m *middlewareStats) Recheck(ctx context.Context, appConn proxy.AppConnMempool) (OpResult, error) {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "Recheck")
	return m.next.Recheck(ctx, appConn)
}

func (m *middlewareStats) Remove(ctx context.Context, opts RemOption) (OpResult, error) {
	ctx = withObservations(ctx)
	defer m.observeOp(ctx, "Remove",
		"num_collections", opts.Collections.Count(),
		"num_tx_keys", len(opts.TxKeys),
	)
	return m.next.Remove(ctx, opts)
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