package mempool

import (
	"context"
	"fmt"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

// Mempool defines the mempool interface.
//
// Updates to the mempool need to be synchronized with committing a block so
// apps can reset their transient state on Commit.
type (
	Mempool[T TXReaper] interface {
		// CheckTx executes a new transaction against the application to determine
		// its validity and whether it should be added to the mempool.
		CheckTx(tx types.Tx, callback func(*abci.Response), txInfo TxInfo) error

		// PrepBlockFinality prepares the mempool for finalizing a block. This may require
		// locking or other concerns that must be handled before a block is safe to commit.
		PrepBlockFinality(ctx context.Context) (finishFn func(), err error)

		// Reap allows the caller to reap certificate digests used for proposal. Callers
		// can choose whatever predicate suits their needs. All predicates are ANDed, aka
		// all set predicates must be satisfied. If no options are provided, then all TXs
		// will be returned.
		Reap(ctx context.Context, opts ...ReapOptFn) (T, error)

		// Update informs the mempool that the given txs were committed and can be discarded.
		// NOTE: this should be called *after* block is committed by consensus.
		// NOTE: Lock/Unlock must be managed by caller
		Update(
			blockHeight int64,
			blockTxs types.Txs,
			deliverTxResponses []*abci.ResponseDeliverTx,
			newPreFn PreCheckFunc,
			newPostFn PostCheckFunc,
		) error

		// Flush removes all transactions from the mempool and cache
		Flush()

		// TxsAvailable returns a channel which fires once for every height,
		// and only when transactions are available in the mempool.
		// NOTE: the returned channel may be nil if EnableTxsAvailable was not called.
		TxsAvailable() <-chan struct{}

		// EnableTxsAvailable initializes the TxsAvailable channel, ensuring it will
		// trigger once every height when transactions are available.
		EnableTxsAvailable()

		// Meta returns metadat for the mempool.
		Meta() Meta

		// InitWAL creates a directory for the WAL file and opens a file itself. If
		// there is an error, it will be of type *PathError.
		InitWAL() error

		// CloseWAL closes and discards the underlying WAL file.
		// Any further writes will not be relayed to disk.
		CloseWAL()
	}

	// TXReaper is the output of a Reap query for a mempool implemenation. Examples of
	// this are for hte existing mempools, where we return Txs, and the DAG mempools which
	// use DAG
	TXReaper interface {
		// TXs retrieves TXs for the given DAGCerts. It will take TXs from the
		// RootCert and ExtraCerts in addition to all the TXs from the descandant
		// certificates of the RootCert.
		TXs(ctx context.Context) (types.Txs, error)
	}
)

// DisableReapOpt sets the reap opt to disabled. This is the default value for all
// fields in the ReapOption type. If you call Reap(ctx), you will get all TXs within
// the mempool (if allowed).
const DisableReapOpt = -1

// ReapOption is the options to reaping a collection useful to proposal from the mempool.
// A collection for proposal in teh Clist or Priority mempools, would be a list of TXs.
// For a DAG (narwhal) mempool, we'd want the DAGCerts for the proposal. However,
// we use the same predicates to reap from all mempools. When a predicate has multiple opts
// specified, will take the collections that satisfy all limits specified are adhered too.
// When a field is set to DisablePredicate (-1), the field will not be enforced.
//
// Example predicate: BlockSizeLimit AND NumTXs are both set enforcing that BlockSizeLimit
//					  and NumTXs limits are satisfied in the Reaping.
type ReapOption struct {
	BlockSizeLimit int64
	GasLimit       int64
	NumTXs         int
}

// CoalesceReapOpts provides a quick way to coalesce ReapOptFn's with default field
// values set to DisableReapOpt.
func CoalesceReapOpts(opts ...ReapOptFn) ReapOption {
	opt := ReapOption{
		BlockSizeLimit: DisableReapOpt,
		GasLimit:       DisableReapOpt,
		NumTXs:         DisableReapOpt,
	}
	for _, o := range opts {
		o(&opt)
	}
	return opt
}

// ReapOptFn is a functional option for setting the reap predicates.
type ReapOptFn func(*ReapOption)

// ReapBytes will limit the reap by a maximum number of bytes. Note, if
// you provide a value less than 0, it will ignore the max bytes limit.
// This is the same as if you did not provide the option to the Reap method.
func ReapBytes(maxBytes int64) ReapOptFn {
	return func(option *ReapOption) {
		option.BlockSizeLimit = maxBytes
	}
}

// ReapGas will limit the reap by the maxGas. Note, if you provide a
// value less than 0, it will ignore the gas limit. This is the same as if
// you did not provide the option to the Reap method.
func ReapGas(maxGas int64) ReapOptFn {
	return func(option *ReapOption) {
		option.GasLimit = maxGas
	}
}

// ReapTXs will limit the reap to a number of TXs. Note, if you provide
// a value less than 0, it will ignore the TXs. This is the same as if
// you did not provide the option to the Reap method.
func ReapTXs(maxTXs int) ReapOptFn {
	return func(option *ReapOption) {
		option.NumTXs = maxTXs
	}
}

// Meta provides metadat regarding a mempool. The size and TXsBytes being >= 0
// indicates the metadata is valid for the given mempool.
type Meta struct {
	Size     int
	TXsBytes int64
}

//--------------------------------------------------------------------------------

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

//--------------------------------------------------------------------------------

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
