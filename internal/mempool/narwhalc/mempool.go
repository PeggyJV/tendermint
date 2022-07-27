package narwhalc

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/internal/mempool"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

var (
	errNilCerts = errors.New("invalid nil-Certificates provided")
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

type Mempool struct {
	// narwhal clients
	primaryC              *primaryClient
	workersC              []*workerClient
	workerSubmitTXTimeout time.Duration

	// dependencies
	logger         log.Logger
	workerSelectFn func() *workerClient
}

var _ mempool.Pool = (*Mempool)(nil)

func New(ctx context.Context, cfg *config.NarwhalMempoolConfig, opts ...MempoolOption) (*Mempool, error) {
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
	}
	for _, o := range opts {
		o(c)
	}

	return c, nil
}

func (mem *Mempool) CheckTxCallback(ctx context.Context, tx types.Tx, res *abci.ResponseCheckTx, txInfo mempool.TxInfo) (mempool.OpResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mem.workerSubmitTXTimeout)
	defer cancel()

	if err := mem.submitTx(ctx, tx); err != nil {
		return mempool.OpResult{RemovedTxs: types.Txs{tx}}, err
	}

	return mempool.OpResult{Status: mempool.StatusTxsAvailable}, nil
}

func (mem *Mempool) Flush(ctx context.Context) error {
	return nil
}

func (mem *Mempool) HydrateBlockTxs(ctx context.Context, block *types.Block) error {
	if block.Txs != nil {
		// work here has already been done
		return nil
	}

	certs := block.Certificates
	if certs == nil {
		return errNilCerts
	}

	dc := DAGCerts{
		RootCert: CertificateDigest(certs.RootCert),
	}
	for i := range certs.ExtraCerts {
		dc.ExtraCerts = append(dc.ExtraCerts, CertificateDigest(certs.ExtraCerts[i]))
	}

	txs, err := dc.Txs(ctx)
	if err != nil {
		return err
	}

	// TODO(berg): if we decide to make room for heterogeneous proposals, we'll
	//				need to make sure that the data.Txs is added here and we
	//				establish the correct order. Right now, we're just setting
	//				the Txs member.
	block.Txs = txs

	return nil
}

func (mem *Mempool) Meta() mempool.PoolMeta {
	return mempool.PoolMeta{
		Type: "narwhal",
		// These are not available at this time
		Size:       -1,
		TotalBytes: -1,
	}
}

func (mem *Mempool) OnBlockFinality(ctx context.Context, block *types.Block, _ mempool.PostCheckFunc) (mempool.OpResult, error) {
	if block.Certificates == nil {
		return mempool.OpResult{}, errNilCerts
	}

	certs := make([]CertificateDigest, 0, 1+len(block.Certificates.ExtraCerts))
	certs = append(certs, CertificateDigest(block.Certificates.RootCert))
	for i := range block.Certificates.ExtraCerts {
		certs = append(certs, CertificateDigest(block.Certificates.ExtraCerts[i]))
	}
	err := mem.primaryC.RemoveCollections(ctx, certs...)
	if err != nil {
		return mempool.OpResult{}, err
	}
	return mempool.OpResult{
		Status: mempool.StatusTxsAvailable,
	}, nil
}

func (mem *Mempool) Reap(ctx context.Context, optFns ...mempool.ReapOptFn) (types.TxReaper, error) {
	dagCerts, err := mem.primaryC.NextBlockCerts(ctx, mempool.CoalesceReapOpts(optFns...))
	if err != nil {
		return DAGCerts{}, fmt.Errorf("failed to obtain collection certs: %w", err)
	}
	return dagCerts, nil
}

func (mem *Mempool) AfterBlockFinality(ctx context.Context, block *types.Block, deliverTxResponses []*abci.ResponseDeliverTx) error {
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

	if block.Certificates == nil {
		return errNilCerts
	}

	certs := make([]CertificateDigest, 0, 1+len(block.Certificates.ExtraCerts))
	certs = append(certs, CertificateDigest(block.Certificates.RootCert))
	for i := range block.Certificates.ExtraCerts {
		certs = append(certs, CertificateDigest(block.Certificates.ExtraCerts[i]))
	}
	return mem.primaryC.RemoveCollections(ctx, certs...)
}

func (mem *Mempool) Remove(ctx context.Context, opts ...mempool.RemOptFn) (mempool.OpResult, error) {
	panic("not implemented yet")
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
