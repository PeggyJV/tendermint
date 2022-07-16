package narwhalc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

// MempoolOption is a functional option used to set optional params on the Mempool mempool.
type MempoolOption func(*Mempool)

// PreCheckFunc is an optional filter executed before CheckTx and rejects
// transaction if false is returned. An example would be to ensure that a
// transaction doesn't exceed the block size.
type PreCheckFunc func(types.Tx) error

func WithLogger(logger log.Logger) MempoolOption {
	return func(mempool *Mempool) {
		mempool.logger = logger
	}
}

func WithPreCheckFunc(fn PreCheckFunc) MempoolOption {
	return func(mempool *Mempool) {
		mempool.precheckFn = fn
	}
}

// TODO(berg): find better name for Mempool here

type Mempool struct {
	// narwhal clients
	primaryC              *primaryClient
	workersC              []*workerClient
	workerSubmitTXTimeout time.Duration

	// dependencies
	logger       log.Logger
	precheckFn   PreCheckFunc
	proxyAppConn proxy.AppConnMempool

	workerSelectFn func() *workerClient
}

var _ mempool.Pool[DAGCerts] = (*Mempool)(nil)

func New(ctx context.Context, proxyAppCon proxy.AppConnMempool, cfg *config.NarwhalMempoolConfig, opts ...MempoolOption) (*Mempool, error) {
	var workersC []*workerClient
	for workerLabel, workerCFG := range cfg.Workers {
		workerC, err := newWorkerClient(ctx, workerCFG.EncodedPublicKey, workerCFG.Addr, workerLabel)
		if err != nil {
			return nil, fmt.Errorf("failed to create narwhal worker node client: %w", err)
		}
		workersC = append(workersC, workerC)
	}

	primaryC, err := newPrimaryClient(ctx, cfg.PrimaryEncodedPublicKey, cfg.PrimaryAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create narwhal primary node client: %w", err)
	}

	c := &Mempool{
		logger:         log.NewNopLogger(),
		primaryC:       primaryC,
		workersC:       workersC,
		workerSelectFn: newRoundRobinWorkerSelectFn(workersC),
		precheckFn: func(tx types.Tx) error {
			return nil
		},
		proxyAppConn: proxyAppCon,
	}
	for _, o := range opts {
		o(c)
	}

	return c, nil
}

func (mem *Mempool) CheckTX(ctx context.Context, tx types.Tx, cb func(checkTx *abci.Response), txInfo mempool.TxInfo) error {
	// TODO(berg): this looks like a place where we could pull out. The
	//			   execution of PreCheck is not a mempool specific thing.
	//			   Well... perhaps it is, worth a look.
	if err := mem.precheckFn(tx); err != nil {
		return mempool.ErrPreCheck{Reason: err}
	}

	// NOTE: proxyAppConn may error if tx buffer is full
	if err := mem.proxyAppConn.Error(); err != nil {
		return err
	}

	reqRes := mem.proxyAppConn.CheckTxAsync(abci.RequestCheckTx{Tx: tx})
	reqRes.SetCallback(func(res *abci.Response) {
		r, ok := res.Value.(*abci.Response_CheckTx)
		if !ok {
			// ignore unmatched messages
			return
		}

		if code := r.CheckTx.Code; code != abci.CodeTypeOK {
			// ignore bad transaction
			mem.logger.Debug("rejected bad transaction",
				"tx", txID(tx),
				"peerID", txInfo.SenderP2PID,
				"res", r,
				"err", "received invalid code",
			)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), mem.workerSubmitTXTimeout)
		defer cancel()

		err := mem.submitTx(ctx, tx)
		if err != nil {
			mem.logger.Error("failed to submit TX", "err", err)
		}

		if cb != nil {
			cb(res)
		}
	})

	return nil
}

func (mem *Mempool) Reap(ctx context.Context, opts mempool.ReapOpts) (DAGCerts, error) {
	if err := opts.OK(); err != nil {
		return DAGCerts{}, fmt.Errorf("invalid reap opts provided: %w", err)
	}

	dagCerts, err := mem.primaryC.NextBlockCerts(ctx, opts)
	if err != nil {
		return DAGCerts{}, fmt.Errorf("failed to obtain collections: %w", err)
	}
	return dagCerts, nil
}

func (mem *Mempool) AfterBlockFinality(ctx context.Context, _ int64, in DAGCerts, deliverTxResponses []*abci.ResponseDeliverTx) error {
	for _, resp := range deliverTxResponses {
		if resp.Code != abci.CodeTypeOK {
			mem.logger.With("tx_info", resp.Info).Error("failed to deliver TX to application")
		}
		// TODO(berg): we have no means to know if a Tx in its entirety was submitted. We can
		//			   determine if a collection's batches contain a given TX... however, if it
		//			   does, what do we want to do with that TX?
		//
		//			   Thoughts, if this failed b/c the TX is invalid, then we just dump in on the
		//			   ground... however, if it fails for some transient reason (socket blipp/etc),
		//			   then we probably want another way to resubmit that Tx. However, given how the
		//			   narwhal cluster is separated, we won't have any way to guarantee it is included
		//			   before a Tx that may have a later sequence, invalidating the TX
	}

	return mem.primaryC.RemoveCollections(ctx, append(in.ExtraCerts, in.RootCert)...)
}

func (mem *Mempool) submitTx(ctx context.Context, tx types.Tx) error {
	workerC := mem.workerSelectFn()
	return workerC.SubmitTransaction(ctx, tx)
}

// newRoundRobinWorkerSelectFn is safe for concurrent access.
func newRoundRobinWorkerSelectFn(workers []*workerClient) func() *workerClient {
	var i uint64
	return func() *workerClient {
		nextWorker := atomic.AddUint64(&i, 1) % uint64(len(workers))
		return workers[nextWorker]
	}
}
