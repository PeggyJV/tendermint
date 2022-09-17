package mock

import (
	"context"

	abci "github.com/tendermint/tendermint/abci/types"
	mempl "github.com/tendermint/tendermint/mempool"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
)

// Mempool is an empty implementation of a Mempool, useful for testing.
type Mempool struct{}

var _ sm.Mempool = Mempool{}

func (m Mempool) AfterBlockFinality(ctx context.Context, block *types.Block, txResults []*abci.ResponseDeliverTx, newPreFn mempl.PreCheckFunc, newPostFn mempl.PostCheckFunc) error {
	return nil
}

func (m Mempool) NewHydratedBlock(ctx context.Context, block *types.Block) (*types.Block, error) {
	bb := types.Block{
		Header:   block.Header,
		Data:     block.Data,
		Evidence: block.Evidence,
	}
	if block.LastCommit != nil {
		lc := *block.LastCommit
		bb.LastCommit = &lc
	}
	return &bb, nil
}

func (m Mempool) PrepBlockFinality(_ context.Context) (func(), error) {
	return func() {}, nil
}

func (m Mempool) Reap(ctx context.Context, opts ...mempl.ReapOptFn) (types.Data, error) {
	return types.Data{}, nil
}
