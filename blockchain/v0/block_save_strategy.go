package v0

import (
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
)

type defaultBlockSaveStrategy struct{}

func (d defaultBlockSaveStrategy) SaveBlock(bs sm.BlockStore, bl *types.Block, fullPS *types.PartSet, commit *types.Commit) {
	bs.SaveBlock(bl, fullPS, commit)
}
