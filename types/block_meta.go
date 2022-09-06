package types

import (
	"bytes"
	"errors"
	"fmt"

	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

// BlockMeta contains meta information.
type BlockMeta struct {
	BlockID                BlockID       `json:"block_id"`
	BlockSize              int           `json:"block_size"`
	ConsensusPartSetHeader PartSetHeader `json:"consensus_part_set_header"`
	Header                 Header        `json:"header"`
	NumTxs                 int           `json:"num_txs"`
}

// NewBlockMeta returns a new BlockMeta.
func NewBlockMeta(block *Block, blockParts *PartSet) *BlockMeta {
	return NewBlockMetaV2(block, blockParts, PartSetHeader{})
}

func NewBlockMetaV2(block *Block, blockParts *PartSet, consensusPartSetHeader PartSetHeader) *BlockMeta {
	return &BlockMeta{
		BlockID:                BlockID{block.Hash(), blockParts.Header()},
		BlockSize:              block.Size(),
		ConsensusPartSetHeader: consensusPartSetHeader,
		Header:                 block.Header,
		NumTxs:                 len(block.Data.Txs),
	}
}

func (bm *BlockMeta) ToProto() *tmproto.BlockMeta {
	if bm == nil {
		return nil
	}

	return &tmproto.BlockMeta{
		BlockID:                bm.BlockID.ToProto(),
		BlockSize:              int64(bm.BlockSize),
		ConsensusPartSetHeader: bm.ConsensusPartSetHeader.ToProto(),
		Header:                 *bm.Header.ToProto(),
		NumTxs:                 int64(bm.NumTxs),
	}
}

func BlockMetaFromProto(pb *tmproto.BlockMeta) (*BlockMeta, error) {
	if pb == nil {
		return nil, errors.New("blockmeta is empty")
	}

	bi, err := BlockIDFromProto(&pb.BlockID)
	if err != nil {
		return nil, err
	}

	h, err := HeaderFromProto(&pb.Header)
	if err != nil {
		return nil, err
	}

	psh, err := PartSetHeaderFromProto(&pb.ConsensusPartSetHeader)
	if err != nil {
		return nil, err
	}

	bm := &BlockMeta{
		BlockID:                *bi,
		BlockSize:              int(pb.BlockSize),
		ConsensusPartSetHeader: *psh,
		Header:                 h,
		NumTxs:                 int(pb.NumTxs),
	}

	return bm, bm.ValidateBasic()
}

// ValidateBasic performs basic validation.
func (bm *BlockMeta) ValidateBasic() error {
	if err := bm.BlockID.ValidateBasic(); err != nil {
		return err
	}
	if !bytes.Equal(bm.BlockID.Hash, bm.Header.Hash()) {
		return fmt.Errorf("expected BlockID#Hash and Header#Hash to be the same, got %X != %X",
			bm.BlockID.Hash, bm.Header.Hash())
	}
	return nil
}
