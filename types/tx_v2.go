package types

import (
	"context"
	"crypto/sha256"
)

// TxKey is the fixed length array key used as an index.
type TxKey [sha256.Size]byte

// TxReaper returns a list of txs.
type TxReaper interface {
	Txs(context.Context) (Txs, error)
	// TODO(berg): should we make this take a *Data and mutate it instead? :thinking_face:
	BlockData() Data
}

func (txs Txs) Txs(ctx context.Context) (Txs, error) {
	return txs, nil
}

func (txs Txs) BlockData() Data {
	return Data{Txs: txs}
}
