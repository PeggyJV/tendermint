package narwhal

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalc"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

// PoolOption is a functional option used to set optional params on the pool.
type PoolOption func(pool *Pool)

func WithLogger(logger log.Logger) PoolOption {
	return func(mempool *Pool) {
		mempool.logger = logger
	}
}

func WithPreCheckFunc(fn mempool.PreCheckFunc) PoolOption {
	return func(mempool *Pool) {
		mempool.precheckFn = fn
	}
}

type Pool struct {
	// narwhal clients
	primaryC              *narwhalc.PrimaryClient
	workersC              []*narwhalc.WorkerClient
	workerSubmitTXTimeout time.Duration

	// dependencies
	logger         log.Logger
	precheckFn     mempool.PreCheckFunc
	postcheckFn    mempool.PostCheckFunc
	workerSelectFn func() *narwhalc.WorkerClient
}

var _ mempool.Pool = (*Pool)(nil)

func New(ctx context.Context, cfg *config.NarwhalMempoolConfig, opts ...PoolOption) (*Pool, error) {
	var workersC []*narwhalc.WorkerClient
	for _, wCFG := range cfg.Workers {
		workerC, err := narwhalc.NewWorkerClient(ctx, wCFG.Addr, wCFG.Name)
		if err != nil {
			return nil, err
		}
		workersC = append(workersC, workerC)
	}

	p := Pool{
		workersC:       workersC,
		logger:         log.NewNopLogger(),
		precheckFn:     func(tx types.Tx) error { return nil },
		postcheckFn:    func(tx types.Tx, resp *abci.ResponseCheckTx) error { return nil },
		workerSelectFn: newRoundRobinWorkerSelectFn(workersC),
	}
	for _, o := range opts {
		o(&p)
	}

	primaryC, err := narwhalc.NewPrimaryClient(
		ctx,
		p.logger.With("client", "narwhal_primary"),
		cfg.PrimaryEncodedPublicKey,
		cfg.PrimaryAddr,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create narwhal primary node client: %w", err)
	}
	p.primaryC = primaryC

	return &p, nil
}

func (p *Pool) CheckTxCallback(ctx context.Context, tx types.Tx, res *abci.ResponseCheckTx, txInfo mempool.TxInfo) mempool.OpResult {
	start := time.Now()
	defer func() {
		p.logger.Debug("submit tx to narwhal", "took", time.Since(start).String())
	}()

	workerC := p.workerSelectFn()
	err := workerC.SubmitTransaction(ctx, tx, res.GasWanted)
	if err != nil {
		p.logger.Error("failed to submit transaction to narwhal")
		return mempool.OpResult{RemovedTxs: types.Txs{tx}}
	}
	return mempool.OpResult{Status: mempool.StatusTxsAvailable}
}

func (p *Pool) CheckTxPrep(ctx context.Context, tx types.Tx) error {
	if p.precheckFn == nil {
		return nil
	}
	return p.precheckFn(tx)
}

func (p *Pool) Flush(ctx context.Context) error {
	return nil
}

func (p *Pool) GlobalCheck(tx types.Tx, res *abci.ResponseCheckTx) (mempool.OpResult, error) {
	return mempool.OpResult{}, nil
}

func (p *Pool) HydrateBlockData(ctx context.Context, bl *types.Block) (types.Data, error) {
	txs, err := p.txsFromColls(ctx, bl.Collections)
	if err != nil {
		return types.Data{}, err
	}
	return types.Data{Txs: txs}, nil
}

func (p *Pool) Meta() mempool.PoolMeta {
	return mempool.PoolMeta{
		Type:       "narwhal",
		Size:       -1,
		TotalBytes: -1,
	}
}

func (p *Pool) OnBlockFinality(ctx context.Context, block *types.Block, newPreFn mempool.PreCheckFunc, newPostFn mempool.PostCheckFunc) (mempool.OpResult, error) {
	if newPreFn != nil {
		p.precheckFn = newPreFn
	}
	if newPostFn != nil {
		p.postcheckFn = newPostFn
	}

	coll := block.Collections
	if coll == nil {
		return mempool.OpResult{}, errors.New("invalid <nil> block collections at OnBlockFinality")
	}

	if err := p.primaryC.RemoveDAGCollections(ctx, *coll); err != nil {
		return mempool.OpResult{}, err
	}

	return mempool.OpResult{Status: mempool.StatusTxsAvailable}, nil
}

func (p *Pool) Reap(ctx context.Context, opts mempool.ReapOption) (mempool.ReapResults, error) {
	collections, err := p.nextBlockCerts(ctx, opts)
	if err != nil {
		return mempool.ReapResults{}, err
	}

	txs, err := p.txsFromColls(ctx, collections)
	if err != nil {
		return mempool.ReapResults{}, err
	}

	return mempool.ReapResults{
		Collections: collections,
		Txs:         txs,
	}, nil
}

func (p *Pool) nextBlockCerts(ctx context.Context, opts mempool.ReapOption) (colls *types.DAGCollections, _ error) {
	start := time.Now()
	defer func() {
		p.logger.Info("next block certs obtained", "num_collections", colls.Count(), "took", time.Since(start).String())
	}()

	collections, err := p.primaryC.NextBlockCerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain collections: %w", err)
	}
	return collections, nil
}

func (p *Pool) Recheck(ctx context.Context, appConn proxy.AppConnMempool) (mempool.OpResult, error) {
	// noop
	return mempool.OpResult{}, nil
}

func (p *Pool) Remove(ctx context.Context, opts mempool.RemOption) (mempool.OpResult, error) {
	start := time.Now()
	defer func() {
		p.logger.Info("remove certs obtained", "num_collections", opts.Collections.Count(), "took", time.Since(start).String())
	}()

	if opts.Collections == nil {
		return mempool.OpResult{}, nil
	}

	if err := p.primaryC.RemoveDAGCollections(ctx, *opts.Collections); err != nil {
		return mempool.OpResult{}, err
	}

	return mempool.OpResult{Status: mempool.StatusTxsAvailable}, nil
}

func (p *Pool) txsFromColls(ctx context.Context, coll *types.DAGCollections) (txs types.Txs, _ error) {
	start := time.Now()
	defer func() {
		p.logger.Info("txs from colls obtained", "num_colls", coll.Count(), "num_txs", len(txs), "took", time.Since(start).String())
	}()
	if coll == nil {
		return nil, errors.New("invalid <nil> Collections for hydrating block data")
	}

	txs, err := p.primaryC.DAGCollectionTXs(ctx, *coll)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

// newRoundRobinWorkerSelectFn is safe for concurrent access.
func newRoundRobinWorkerSelectFn(workers []*narwhalc.WorkerClient) func() *narwhalc.WorkerClient {
	var i uint64
	return func() *narwhalc.WorkerClient {
		nextWorker := atomic.AddUint64(&i, 1) % uint64(len(workers))
		return workers[nextWorker]
	}
}
