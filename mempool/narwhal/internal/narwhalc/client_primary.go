package narwhalc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"strings"
	"time"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalproto"
	"github.com/tendermint/tendermint/observe"
	"github.com/tendermint/tendermint/types"
)

// PrimaryClient is the grpc Validator service client with additional
// cluster information for debugging/error handling.
type PrimaryClient struct {
	logger    log.Logger
	publicKey []byte

	vc narwhalproto.ValidatorClient
	pc narwhalproto.ProposerClient
	clientBase
}

// NewPrimaryClient constructs a primary node client from the gprc conn and the additional
// metadata used to identify the narwhal node.
func NewPrimaryClient(ctx context.Context, logger log.Logger, nodeEncodedPK, addr string) (*PrimaryClient, error) {
	cc, err := newGRPCConnection(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection for primary with addr(%s): %w", addr, err)
	}

	publicKey, err := base64.StdEncoding.DecodeString(nodeEncodedPK)
	if err != nil {
		return nil, fmt.Errorf("failed to base64 decode public key from node name(%s): %w", nodeEncodedPK, err)
	}

	return &PrimaryClient{
		logger:    logger,
		publicKey: publicKey,
		vc:        narwhalproto.NewValidatorClient(cc),
		pc:        narwhalproto.NewProposerClient(cc),
		clientBase: clientBase{
			meta: NodeMeta{
				Name: nodeEncodedPK,
				Type: "primary",
				Addr: addr,
			},
		},
	}, nil
}

func (p *PrimaryClient) DAGCollectionTXs(ctx context.Context, colls types.DAGCollections) (types.Txs, error) {
	ctx = observe.WithTraceID(ctx)

	causalColls, err := p.certsFrom(ctx, hexBytesToProtoCert(colls.RootCollection))
	if err != nil {
		return nil, fmt.Errorf("failed to traverse DAG from starting root collection %s", string(colls.RootCollection))
	}

	queryable := append(causalColls, mapSlice(colls.ExtraCollections, hexBytesToProtoCert)...)

	txs, err := p.certTxs(ctx, queryable...)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain TXs for colls: %w", err)
	}

	return txs, nil
}

// certTxs returns the transactions associated to the given collections.
func (p *PrimaryClient) certTxs(ctx context.Context, collDigests ...*narwhalproto.CertificateDigest) (types.Txs, error) {
	pairs := []any{"num_input_coll_digests", len(collDigests)}
	logFn := logDurs(ctx, p.logger, "certTxs")
	defer func() { logFn(pairs...) }()

	if len(collDigests) == 0 {
		return nil, nil
	}

	resp, err := narwhalGetCollections(ctx, p.logger, p.vc, collDigests)
	if err != nil {
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	txs, err := takeTxsFromCollectionsResult(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to take txs from collection results: %w", err)
	}
	pairs = append(pairs, "num_txs", len(txs))

	var out types.Txs
	for _, tx := range txs {
		var txf txFull
		err := gob.
			NewDecoder(bytes.NewBuffer(tx.Transaction)).
			Decode(&txf)
		if err != nil {
			return nil, err
		}
		out = append(out, txf.Tx)
	}
	return out, nil
}

func (p *PrimaryClient) RemoveDAGCollections(ctx context.Context, colls types.DAGCollections) error {
	ctx = observe.WithTraceID(ctx)
	defer logDurs(ctx, p.logger, "RemoveDAGCollections")("num_colls", colls.Count())

	allColls, err := p.dagCollectionProtoCerts(ctx, colls)
	if err != nil {
		return err
	}
	return p.removeCollections(ctx, allColls...)
}

func (p *PrimaryClient) dagCollectionProtoCerts(ctx context.Context, colls types.DAGCollections) ([]*narwhalproto.CertificateDigest, error) {
	pairs := []any{"num_colls", colls.Count()}
	logFn := logDurs(ctx, p.logger, "dagCollectionProtoCerts")
	defer func() { logFn(pairs...) }()

	rootTreeCollections, err := p.certsFrom(ctx, hexBytesToProtoCert(colls.RootCollection))
	if err != nil {
		return nil, err
	}

	rootTreeCollections = append(rootTreeCollections, mapSlice(colls.ExtraCollections, hexBytesToProtoCert)...)
	return rootTreeCollections, nil
}

// certsFrom returns a list of transactions starting from the provided argument and walking the DAG
// in BFS fashion to obtain all child collections.
func (p *PrimaryClient) certsFrom(ctx context.Context, startingCollection *narwhalproto.CertificateDigest) ([]*narwhalproto.CertificateDigest, error) {
	resp, err := p.vc.ReadCausal(ctx, &narwhalproto.ReadCausalRequest{
		CollectionId: startingCollection,
	})
	if err != nil {
		return nil, fmt.Errorf("failed read causal: %w", err)
	}
	return resp.CollectionIds, nil
}

// removeCollections removes collections from existence. This is run after consensus as the
// collections are no longer needed.
func (p *PrimaryClient) removeCollections(ctx context.Context, collDigests ...*narwhalproto.CertificateDigest) error {
	_, err := p.vc.RemoveCollections(ctx, &narwhalproto.RemoveCollectionsRequest{
		CollectionIds: collDigests,
	})
	if err != nil {
		return fmt.Errorf("failed remove collections: %w", err)
	}
	return nil
}

// NextBlockCerts provides the collection ID of the starting collection ID to be used with readCause while
// also returning the additional collection IDs that can be added to the block from later rounds that may be sibling
// and/or not included from the BFS from the starting collection ID via ReadCausal.
func (p *PrimaryClient) NextBlockCerts(ctx context.Context, opts mempool.ReapOption) (*types.DAGCollections, error) {
	ctx = observe.WithTraceID(ctx)
	defer logDurs(ctx, p.logger, "NextBlockCerts")()

	causalCollection, collections, err := nextBlockCollections(ctx, nextBlockIn{
		opts:      opts,
		logger:    p.logger,
		publicKey: p.publicKey,
		pc:        p.pc,
		vc:        p.vc,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get next block collections: %w", err)
	}

	return &types.DAGCollections{
		RootCollection:   protoCertDigestToHexBytes(causalCollection),
		ExtraCollections: mapSlice(collections, protoCertDigestToHexBytes),
	}, nil
}

type nextBlockIn struct {
	opts      mempool.ReapOption
	logger    log.Logger
	publicKey []byte

	pc narwhalproto.ProposerClient
	vc narwhalproto.ValidatorClient
}

func nextBlockCollections(ctx context.Context, in nextBlockIn) (causalCollection *narwhalproto.CertificateDigest, collections []*narwhalproto.CertificateDigest, _ error) {
	prep := &blockPrep{
		blockGasLimit: in.opts.BlockSizeLimit,
		blockTXsLimit: int64(in.opts.NumTxs),
		logger:        in.logger,
		publicKey: narwhalproto.PublicKey{
			Bytes: in.publicKey,
		},
		pc:               in.pc,
		vc:               in.vc,
		mSeenCollections: make(map[string]bool),
	}
	return prep.nextBlockCollections(ctx)
}

type blockPrep struct {
	blockSizeBytesLimit int64
	blockGasLimit       int64
	blockTXsLimit       int64
	logger              log.Logger
	publicKey           narwhalproto.PublicKey

	pc narwhalproto.ProposerClient
	vc narwhalproto.ValidatorClient

	mSeenCollections map[string]bool
}

func (b *blockPrep) nextBlockCollections(ctx context.Context) (*narwhalproto.CertificateDigest, []*narwhalproto.CertificateDigest, error) {
	defer logDurs(ctx, b.logger, "nextBlockCollections")()

	roundsResp, err := b.rounds(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to make rounds request for pk(%s): %w", base64Encode(b.publicKey.Bytes), err)
	}

	oldest, mostRecentRound := roundsResp.OldestRound, roundsResp.NewestRound
	currentRound := oldest
	lastCompletedRound, extraCollections, err := b.nextRelevantRound(ctx, currentRound, mostRecentRound)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get relevant block round (start=%d, oldest=%d): %w", oldest, mostRecentRound, err)
	}

	roundCollections, err := b.findRoundCollections(ctx, oldest, lastCompletedRound)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find starting collection ID: %w", err)
	}
	if len(roundCollections) == 0 {
		return nil, nil, fmt.Errorf("failed to obtain a collection for %d round", lastCompletedRound)
	}
	rootCollection := roundCollections[0]

	mColls := make(map[string]bool, len(roundCollections))
	for i := range roundCollections {
		mColls[string(roundCollections[i].Digest)] = true
	}

	var dedupedExtraColls []*narwhalproto.CertificateDigest
	for i := range extraCollections {
		coll := extraCollections[i]
		digest := string(coll.Digest)
		if mColls[digest] {
			continue
		}
		mColls[digest] = true
		dedupedExtraColls = append(dedupedExtraColls, coll)
	}

	return rootCollection, dedupedExtraColls, nil
}

func (b *blockPrep) findRoundCollections(ctx context.Context, startRound, currentRound uint64) ([]*narwhalproto.CertificateDigest, error) {
	if currentRound < startRound {
		return nil, fmt.Errorf("failed to find valid round via node read causal")
	}

	nrcResp, err := b.pc.NodeReadCausal(ctx, &narwhalproto.NodeReadCausalRequest{
		PublicKey: &b.publicKey,
		Round:     currentRound,
	})
	if err != nil {
		if strings.Contains(err.Error(), "No known certificates for this authority") {
			return b.findRoundCollections(ctx, startRound, currentRound-1)
		}
		return nil, fmt.Errorf("failed to node read causal for last completed round: %w", err)
	}
	if len(nrcResp.CollectionIds) == 0 {
		return b.findRoundCollections(ctx, startRound, currentRound-1)
	}

	return nrcResp.CollectionIds, nil
}

func (b *blockPrep) nextRelevantRound(ctx context.Context, currentRound, mostRecentRound uint64) (lastCompletedRound uint64, extraCollections []*narwhalproto.CertificateDigest, _ error) {
	pairs := []any{"current_round", currentRound, "most_recent_round", mostRecentRound}
	logFn := logDurs(ctx, b.logger, "nextRelevantRound")
	defer func() { logFn(pairs...) }()

	lastCompletedRound, extraCollections, totalTxs, err := b.traverseRounds(
		ctx,
		currentRound, mostRecentRound,
		0, 0, 0,
		nil,
	)
	if err != nil {
		return 0, nil, err
	}
	pairs = append(pairs,
		"last_completed_round", lastCompletedRound,
		"num_extra_colls", len(extraCollections),
		"total_txs", totalTxs,
	)

	if totalTxs == 0 {
		return 0, nil, fmt.Errorf("no txs associated with available DAG rounds")
	}
	return lastCompletedRound, extraCollections, nil
}

func (b *blockPrep) traverseRounds(
	ctx context.Context,
	currentRound, mostRecentRound uint64,
	proposedBlockSize, proposedBlockGasCost, proposedBlockNumTXs int64,
	collections []*narwhalproto.CertificateDigest,
) (lastCompletedRound uint64, extraCollections []*narwhalproto.CertificateDigest, totalTxs int64, _ error) {
	pairs := []any{
		"current_round", currentRound,
		"most_recent_round", mostRecentRound,
		"proposed_block_size", proposedBlockSize,
		"proposed_block_gas_cost", proposedBlockGasCost,
		"proposed_block_num_txs", proposedBlockNumTXs,
		"num_colls", len(collections),
	}
	logFn := logDurs(ctx, b.logger, "traverseRounds")
	defer func() { logFn(pairs...) }()

	readCausalResp, err := b.nodeReadCausal(ctx, currentRound)
	if err != nil {
		if strings.Contains(err.Error(), "No known certificates for this authority") {
			return currentRound - 1, collections, proposedBlockNumTXs, nil
		}
		return 0, nil, 0, fmt.Errorf("failed node read cause for current round %d (most recent rount %d): %w", currentRound, mostRecentRound, err)
	}

	colls, duplicatesRemoved := b.getNewCollectionIDs(readCausalResp.CollectionIds)

	var newColls []*narwhalproto.CertificateDigest
	for _, collection := range colls {
		txs, err := b.getCollectionTxs(ctx, collection)
		if err != nil {
			return 0, nil, 0, fmt.Errorf("failed to get collection txs: %w", err)
		}
		if len(txs) == 0 {
			continue
		}

		stats, err := b.getTxsStats(ctx, txs)
		if err != nil {
			return 0, nil, 0, fmt.Errorf("failed to get txs stats: %w", err)
		}
		proposedBlockSize += stats.sizeBytes
		proposedBlockGasCost += stats.gasWanted
		proposedBlockNumTXs += stats.numTXs
		if b.blockSizeExceed(proposedBlockSize) ||
			b.gasExceeded(proposedBlockGasCost) ||
			b.numTXsExceeded(proposedBlockNumTXs) {
			break
		}

		newColls = append(newColls, collection)
	}

	// our return cases
	switch {
	// case where we are unable to take the entire round of collections. We take the
	// round before and add the extra collections from the current round that will
	// fit within the gas limit.
	case len(newColls)+duplicatesRemoved < len(readCausalResp.CollectionIds):
		return currentRound - 1, newColls, proposedBlockNumTXs, nil
		// if we are one round ahead of the newest round, then we take the extra collections again
		// and set the lastCompletedRound to most recent round. This allows us to take nodes in the DAG
		// that are at the same height as the newest round (the new colls).
	case currentRound == mostRecentRound+1:
		return mostRecentRound, newColls, proposedBlockNumTXs, nil
	}

	// recurse further until we run out of rounds or exceed the gas threshold
	return b.traverseRounds(ctx, currentRound+1, mostRecentRound, proposedBlockSize, proposedBlockGasCost, proposedBlockNumTXs, newColls)
}

func (b *blockPrep) getTxsStats(ctx context.Context, txs []*narwhalproto.Transaction) (out struct{ numTXs, sizeBytes, gasWanted int64 }, _ error) {
	defer logDurs(ctx, b.logger, "getTxsStats")("num_txs", len(txs))

	var buf bytes.Buffer
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		buf.Reset() // just reuse a single buffer to avoid cluttering up the gc
		buf.Write(tx.Transaction)

		// TODO(berg): once proto message is in place, unroll the bytes here instead
		//			   of unmarshalling.
		var txf txFull
		err := gob.
			NewDecoder(&buf).
			Decode(&txf)
		if err != nil {
			return out, err
		}
		out.numTXs++
		out.gasWanted += txf.GasWanted
		out.sizeBytes += int64(len(txf.Tx))
	}
	return out, nil
}

func (b *blockPrep) blockSizeExceed(proposedBytes int64) bool {
	return limitExceeded(b.blockSizeBytesLimit, proposedBytes)
}

func (b *blockPrep) gasExceeded(proposedGas int64) bool {
	return limitExceeded(b.blockGasLimit, proposedGas)
}

func (b *blockPrep) numTXsExceeded(proposedNumTXs int64) bool {
	return limitExceeded(b.blockTXsLimit, proposedNumTXs)
}

func (b *blockPrep) getCollectionTxs(ctx context.Context, collections ...*narwhalproto.CertificateDigest) ([]*narwhalproto.Transaction, error) {
	pairs := []any{"num_collections", len(collections)}
	logFn := logDurs(ctx, b.logger, "getCollectionTxs")
	defer func() { logFn(pairs...) }()

	collectionResp, err := narwhalGetCollections(ctx, b.logger, b.vc, collections)
	if err != nil {
		return nil, fmt.Errorf("failed to get collection IDs: %w", err)
	}

	txs, err := takeTxsFromCollectionsResult(collectionResp)
	if err != nil {
		return nil, fmt.Errorf("failed to map collection results: %w", err)
	}
	pairs = append(pairs, "num_txs", len(txs))

	return txs, nil
}

func (b *blockPrep) getNewCollectionIDs(collectionIDs []*narwhalproto.CertificateDigest) (_ []*narwhalproto.CertificateDigest, numDuplicatesRemoved int) {
	var out []*narwhalproto.CertificateDigest
	for i := range collectionIDs {
		collDigest := string(collectionIDs[i].Digest)
		if b.mSeenCollections[collDigest] {
			numDuplicatesRemoved++
			continue
		}
		out = append(out, collectionIDs[i])
	}
	return out, numDuplicatesRemoved
}

func (b *blockPrep) rounds(ctx context.Context) (*narwhalproto.RoundsResponse, error) {
	logFn := logDurs(ctx, b.logger, "rounds")
	var pairs []any
	defer func() { logFn(pairs...) }()

	resp, err := b.pc.Rounds(ctx, &narwhalproto.RoundsRequest{PublicKey: &b.publicKey})
	if err != nil {
		return nil, err
	}
	pairs = append(pairs, "newest_round", resp.NewestRound, "oldest_round", resp.OldestRound)

	return resp, nil
}

func (b *blockPrep) nodeReadCausal(ctx context.Context, currentRound uint64) (*narwhalproto.NodeReadCausalResponse, error) {
	defer logDurs(ctx, b.logger, "nodeReadCausal")("current_round", currentRound)
	return b.pc.NodeReadCausal(ctx, &narwhalproto.NodeReadCausalRequest{
		PublicKey: &b.publicKey,
		Round:     currentRound,
	})
}

func narwhalGetCollections(ctx context.Context, logger log.Logger, vc narwhalproto.ValidatorClient, collections []*narwhalproto.CertificateDigest) (*narwhalproto.GetCollectionsResponse, error) {
	defer logDurs(ctx, logger, "narwhalGetCollections")("num_collections", len(collections))
	return vc.GetCollections(ctx, &narwhalproto.GetCollectionsRequest{
		CollectionIds: collections,
	})
}

func logDurs(ctx context.Context, logger log.Logger, op string) func(pairs ...any) {
	start := time.Now()
	return func(pairs ...any) {
		took := time.Since(start).Truncate(100 * time.Microsecond).String()
		logInfo(ctx, logger, op, append(pairs, "took", took)...)
	}
}

func logInfo(ctx context.Context, logger log.Logger, op string, pairs ...any) {
	pairs = append(pairs, "trace_id", observe.TraceID(ctx))
	logger.Info(op+" completed", pairs...)
}

func takeTxsFromCollectionsResult(resp *narwhalproto.GetCollectionsResponse) ([]*narwhalproto.Transaction, error) {
	var (
		errs []error
		txs  []*narwhalproto.Transaction
	)
	for i, res := range resp.Result {
		collection, resErr := res.GetCollection(), res.GetError()
		if resErr != nil {
			errs = append(errs, fmt.Errorf("failed collection result for id(%s) idx(%d): %s", base64Encode(resErr.Id.Digest), i, resErr.Error.String()))
			continue
		}
		txs = append(txs, collection.Transactions...)
	}
	return txs, newMultiErr("failed to get txs", errs)
}

func base64Encode(src []byte) string {
	return base64.StdEncoding.EncodeToString(src)
}

func limitExceeded(limit, actual int64) bool {
	return limit > 0 && limit < actual
}
