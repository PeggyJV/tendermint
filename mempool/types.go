package mempool

import (
	"context"
	"fmt"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

// Status is the status of the Pool.
type Status string

const (
	// StatusTxsAvailable indicates the pool has txs available.
	StatusTxsAvailable = "txs_available"
	StatusRecheckReady = "recheck_ready"
)

// Pool defines the underlying pool storage engine behavior.
type Pool interface {
	// CheckTxCallback is called by the ABCI type during CheckTx. When called by ABCI,
	// the CheckTxCallback can assume the ABCI type will hold the app write lock.
	CheckTxCallback(ctx context.Context, tx types.Tx, res *abci.ResponseCheckTx, txInfo TxInfo) OpResult

	CheckTxPrep(ctx context.Context, tx types.Tx) error

	// Flush removes all transactions from the mempool.
	Flush(ctx context.Context) error

	// GlobalCheck will make use of the global callback within the abciclient.Client type.
	GlobalCheck(tx types.Tx, res *abci.ResponseCheckTx) (OpResult, error)

	// HydratedBlockData provides a hook to hydrate a propossed block. In the case of a
	// mempool that obfuscates the txs, like narwhal does with its DAG mempool, representing
	// the txs within consensus as collection certificates, we have to take the
	// block, grab the certificates and find the corresponding txs from that.
	HydratedBlockData(ctx context.Context, block *types.Block) (types.Data, error)

	// Meta returns metadata for the pool.
	Meta() PoolMeta

	// OnBlockFinality is called by the ABCI client after the block has been committed.
	// The ABCI type requires the caller to have called PrepBlockFinality before issuing
	// an Update, thus forcing the caller to acquire the lock before Update and subsequently,
	// this OnBlockFinality call is made.
	OnBlockFinality(ctx context.Context, block *types.Block, newPreFn PreCheckFunc, newPostFn PostCheckFunc) (OpResult, error)

	// Reap returns Txs from the given pool. It is up to the pool implementation to define
	// how they handle the possible predicates from option combinations.
	Reap(ctx context.Context, opts ReapOption) (types.Data, error)

	// Recheck should trigger a recheck of the uncommitted txs within the mempool. Note
	// that not all mempools make use of this. For example, the narwhal mempool does no
	// optimistic rechecks.
	Recheck(ctx context.Context, appConn proxy.AppConnMempool) (OpResult, error)

	// Remove removes txs by the provided RemOptFn. If an argument is provided to the
	// options that don't make sense for the given pool, then it will be ignored.
	Remove(ctx context.Context, opts RemOption) (OpResult, error)
}

// OpResult is the result of a pool operation. This result informs the ABCI type
// what happened in the storage/pool layer, so it can maintain cache coherence.
type OpResult struct {
	RemovedTxs types.Txs
	Status     Status
}

// PoolMeta is the metadata for a given pool.
type PoolMeta struct {
	// Type describes the type of mempool store. Examples could be priority or narwhal.
	Type string
	// Size is the num of txs or whatever the unit of measure for the store.
	Size int
	// TotalBytes is a measure of the store's data size.
	TotalBytes int64
}

// DisableReapOpt sets the reap opt to disabled. This is the default value for all
// fields in the ReapOption type. If you call Reap(ctx), you will get all txs within
// the mempool (if allowed).
const DisableReapOpt = -1

// ReapOption is the options to reaping a collection useful to proposal from the mempool.
// When a predicate has multiple opts specified, will take the collections that satisfy
// all limits specified are adhered too. When a field is set to DisablePredicate (-1),
// the field will not be enforced.
//
// Example predicate: BlockSizeLimit AND GasLimit are both set enforcing that BlockSizeLimit
// and GasLimit limits are satisfied in the Reaping.
type ReapOption struct {
	BlockSizeLimit int64
	GasLimit       int64
	NumTxs         int
	Verify         bool
}

// CoalesceReapOpts provides a quick way to coalesce ReapOptFn's with default field
// values set to DisableReapOpt.
func CoalesceReapOpts(opts ...ReapOptFn) ReapOption {
	opt := ReapOption{
		BlockSizeLimit: DisableReapOpt,
		GasLimit:       DisableReapOpt,
		NumTxs:         DisableReapOpt,
	}
	for _, o := range opts {
		o(&opt)
	}
	return opt
}

// ReapOptFn is a functional option for setting the reap predicates.
type ReapOptFn func(*ReapOption)

// ReapBytes limits the reap by a maximum number of bytes. Note, if you
// provide a value less than 0, it will ignore the max bytes limit. This
// is the same as if you did not provide the option to the Reap method.
func ReapBytes(maxBytes int64) ReapOptFn {
	return func(option *ReapOption) {
		option.BlockSizeLimit = maxBytes
	}
}

// ReapGas limits the reap by the maxGas. Note, if you provide a value
// less than 0, it will ignore the gas limit. This is the same as if you
// did not provide the option to the Reap method.
func ReapGas(maxGas int64) ReapOptFn {
	return func(option *ReapOption) {
		option.GasLimit = maxGas
	}
}

// ReapTxs will limit the reap to a number of txs. Note, if you provide
// a value less than 0, it will ignore the txs. This is the same as if
// you did not provide the option to the Reap method.
func ReapTxs(maxTxs int) ReapOptFn {
	return func(option *ReapOption) {
		option.NumTxs = maxTxs
	}
}

func ReapVerify() ReapOptFn {
	return func(option *ReapOption) {
		option.Verify = true
	}
}

// RemOption is an option for removing txs from a pool.
type RemOption struct {
	TxKeys      []types.TxKey
	Collections *types.DAGCollections
}

// RemOptFn is a functional option definition for setting fields on RemOption.
type RemOptFn func(option *RemOption)

// CoalesceRemOptFns returns a RemOption ready for processing.
func CoalesceRemOptFns(opts ...RemOptFn) RemOption {
	var opt RemOption
	for _, o := range opts {
		o(&opt)
	}
	return opt
}

// RemByTxKeys removes a transaction(s), identified by its key, from the mempool.
func RemByTxKeys(txs ...types.TxKey) RemOptFn {
	return func(option *RemOption) {
		option.TxKeys = append(option.TxKeys, txs...)
	}
}

func RemCollections(coll types.DAGCollections) RemOptFn {
	return func(option *RemOption) {
		option.Collections = &coll
	}
}

// PreCheckFunc is an optional filter executed before CheckTx and rejects
// transaction if false is returned. An example would be to ensure that a
// transaction doesn't exceeded the block size.
type PreCheckFunc func(types.Tx) error

// PostCheckFunc is an optional filter executed after CheckTx and rejects
// transaction if false is returned. An example would be to ensure a
// transaction doesn't require more gas than available for the block.
type PostCheckFunc func(types.Tx, *abci.ResponseCheckTx) error

// TxInfo are parameters that get passed when attempting to add a tx to the
// mempool.
type TxInfo struct {
	// SenderID is the internal peer ID used in the mempool to identify the
	// sender, storing 2 bytes with each tx instead of 20 bytes for the p2p.ID.
	SenderID uint16
	// SenderP2PID is the actual p2p.ID of the sender, used e.g. for logging.
	SenderP2PID p2p.ID
}

// --------------------------------------------------------------------------------

// PreCheckMaxBytes checks that the size of the transaction is smaller or equal to the expected maxBytes.
func PreCheckMaxBytes(maxBytes int64) PreCheckFunc {
	return func(tx types.Tx) error {
		txSize := types.ComputeProtoSizeForTxs([]types.Tx{tx})

		if txSize > maxBytes {
			return fmt.Errorf("tx size is too big: %d, max: %d",
				txSize, maxBytes)
		}
		return nil
	}
}

// PostCheckMaxGas checks that the wanted gas is smaller or equal to the passed
// maxGas. Returns nil if maxGas is -1.
func PostCheckMaxGas(maxGas int64) PostCheckFunc {
	return func(tx types.Tx, res *abci.ResponseCheckTx) error {
		if maxGas == -1 {
			return nil
		}
		if res.GasWanted < 0 {
			return fmt.Errorf("gas wanted %d is negative",
				res.GasWanted)
		}
		if res.GasWanted > maxGas {
			return fmt.Errorf("gas wanted %d is greater than max gas %d",
				res.GasWanted, maxGas)
		}
		return nil
	}
}
