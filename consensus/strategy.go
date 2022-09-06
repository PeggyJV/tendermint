package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/consensus/types"
	"github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
)

type ProposedBlockData struct {
	Block            *types.Block
	BlockID          types.BlockID
	ConsensusPartSet *types.PartSet
}

type Strategy interface {
	ConsensusPartSetFromBlock(block *types.Block) *types.PartSet
	CreateProposalBlock(
		ctx context.Context,
		blockExec *state.BlockExecutor,
		height int64,
		state state.State,
		commit *types.Commit,
		proposerAddr []byte,
	) (ProposedBlockData, error)
	LoadConsensusBlockPart(blockStore state.BlockStore, height int64, idx int) *types.Part
	ConsensusPSHeaderFromBlockMeta(bm *types.BlockMeta) types.PartSetHeader
	SaveBlock(bs state.BlockStore, bl *types.Block, fullPS *types.PartSet, commit *types.Commit)
	SetRoundStateProposalData(ctx context.Context, blockExec *state.BlockExecutor, rs *cstypes.RoundState, block *types.Block) error
}

//--------------------------------------------------------------------

type strategyFull struct{}

var _ Strategy = strategyFull{}

func (s strategyFull) ConsensusPartSetFromBlock(block *types.Block) *types.PartSet {
	return block.MakeDefaultPartSet()
}

func (s strategyFull) ConsensusPSHeaderFromBlockMeta(bm *types.BlockMeta) types.PartSetHeader {
	return bm.BlockID.PartSetHeader
}

func (s strategyFull) CreateProposalBlock(
	ctx context.Context,
	blockExec *state.BlockExecutor,
	height int64,
	state state.State,
	commit *types.Commit,
	proposerAddr []byte,
) (ProposedBlockData, error) {
	block, ps, blockID, err := createBlockProp(ctx, blockExec, height, state, commit, proposerAddr)
	if err != nil {
		return ProposedBlockData{}, err
	}

	return ProposedBlockData{
		Block:            block,
		BlockID:          blockID,
		ConsensusPartSet: ps,
	}, nil
}

func (s strategyFull) LoadConsensusBlockPart(blockStore state.BlockStore, height int64, idx int) *types.Part {
	return blockStore.LoadBlockPart(height, idx)
}

func (s strategyFull) SaveBlock(
	bs state.BlockStore,
	bl *types.Block,
	fullPS *types.PartSet,
	commit *types.Commit,
) {
	bs.SaveBlock(bl, fullPS, commit)
}

func (s strategyFull) SetRoundStateProposalData(
	ctx context.Context,
	blockExec *state.BlockExecutor,
	rs *cstypes.RoundState,
	block *types.Block,
) error {
	rs.ProposalBlock = block
	// in this strategy the consensus part set is the full part set
	rs.ProposalBlockParts = rs.ConsensusPartSet
	return nil
}

//--------------------------------------------------------------------

type strategyMetaOnly struct{}

var _ Strategy = strategyMetaOnly{}

func (s strategyMetaOnly) ConsensusPartSetFromBlock(block *types.Block) *types.PartSet {
	csBlock := types.Block{
		Header:     block.Header,
		Evidence:   block.Evidence,
		LastCommit: block.LastCommit,
	}
	return csBlock.MakeDefaultPartSet()
}

func (s strategyMetaOnly) ConsensusPSHeaderFromBlockMeta(bm *types.BlockMeta) types.PartSetHeader {
	return bm.ConsensusPartSetHeader
}

func (s strategyMetaOnly) CreateProposalBlock(
	ctx context.Context,
	blockExec *state.BlockExecutor,
	height int64,
	state state.State,
	commit *types.Commit,
	proposerAddr []byte,
) (ProposedBlockData, error) {
	block, _, blockID, err := createBlockProp(ctx, blockExec, height, state, commit, proposerAddr)
	if err != nil {
		return ProposedBlockData{}, err
	}

	return ProposedBlockData{
		Block:            block,
		BlockID:          blockID,
		ConsensusPartSet: s.ConsensusPartSetFromBlock(block),
	}, nil
}

func (s strategyMetaOnly) LoadConsensusBlockPart(blockStore state.BlockStore, height int64, idx int) *types.Part {
	return blockStore.LoadBlockConsensusPartSetPart(height, idx)
}

func (s strategyMetaOnly) SaveBlock(
	bs state.BlockStore,
	bl *types.Block,
	fullPS *types.PartSet,
	commit *types.Commit,
) {
	consensusPS := s.ConsensusPartSetFromBlock(bl)
	bs.SaveBlockV2(bl, consensusPS, fullPS, commit)
}

func (s strategyMetaOnly) SetRoundStateProposalData(
	ctx context.Context,
	blockExec *state.BlockExecutor,
	rs *cstypes.RoundState,
	block *types.Block,
) error {
	// since the consensus is speaking the metadata only, we have to hydrate the missing block data
	data, err := blockExec.HydrateBlockData(ctx, block)
	if err != nil {
		return err
	}
	block.Data = data

	//TODO(berg): do we need to reset the data hash now that we have dat hydrated?
	block.DataHash = nil
	block.Hash() // to reset DataHash

	rs.ProposalBlock = block

	// TODO(berg): validate the partset is the valid partset?
	rs.ProposalBlockParts = block.MakeDefaultPartSet()

	return nil
}

func createBlockProp(
	ctx context.Context,
	blockExec *state.BlockExecutor,
	height int64,
	state state.State,
	commit *types.Commit,
	proposerAddr []byte,
) (*types.Block, *types.PartSet, types.BlockID, error) {
	block, err := blockExec.CreateProposalBlock(ctx, height, state, commit, proposerAddr)
	if err != nil {
		return nil, nil, types.BlockID{}, err
	}

	ps := block.MakeDefaultPartSet()
	blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: ps.Header()}

	return block, ps, blockID, nil
}
