package narwhalc

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/tendermint/tendermint/internal/mempool"
	"github.com/tendermint/tendermint/internal/mempool/narwhalc/narwhalproto"
	"github.com/tendermint/tendermint/types"
)

// primaryClient is the grpc Validator service client with additional
// cluster information for debugging/error handling.
type primaryClient struct {
	publicKey []byte

	vc narwhalproto.ValidatorClient
	pc narwhalproto.ProposerClient
	clientBase
}

// newPrimaryClient constructs a primary node client from the gprc conn and the additional
// metadata used to identify the narwhal node.
func newPrimaryClient(ctx context.Context, nodeEncodedPK, addr string) (*primaryClient, error) {
	cc, err := newGRPCConnection(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection for primary with addr(%s): %w", addr, err)
	}

	publicKey, err := base64.StdEncoding.DecodeString(nodeEncodedPK)
	if err != nil {
		return nil, fmt.Errorf("failed to base64 decode public key from node name(%s): %w", nodeEncodedPK, err)
	}

	return &primaryClient{
		vc:        narwhalproto.NewValidatorClient(cc),
		pc:        narwhalproto.NewProposerClient(cc),
		publicKey: publicKey,
		clientBase: clientBase{
			meta: NodeMeta{
				Name: nodeEncodedPK,
				Type: "primary",
				Addr: addr,
			},
		},
	}, nil
}

func (n *primaryClient) DAGCertTXs(ctx context.Context, certs DAGCerts) (types.Txs, error) {
	causalCerts, err := n.certsFrom(ctx, certDigestToProtoCertDigest(certs.RootCert))
	if err != nil {
		return nil, fmt.Errorf("failed to traverse DAG from starting root cert %s", string(certs.RootCert))
	}

	causalCerts = append(causalCerts, mapSlice(certs.ExtraCerts, certDigestToProtoCertDigest)...)

	txs, err := n.certTXs(ctx, causalCerts...)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain TXs for certs: %w", err)
	}

	return txs, nil
}

// certsFrom returns a list of transactions starting from the provided argument and walking the DAG
// in BFS fashion to obtain all child collections.
func (n *primaryClient) certsFrom(ctx context.Context, startingCollection *narwhalproto.CertificateDigest) ([]*narwhalproto.CertificateDigest, error) {
	resp, err := n.vc.ReadCausal(ctx, &narwhalproto.ReadCausalRequest{
		CollectionId: startingCollection,
	})
	if err != nil {
		return nil, fmt.Errorf("failed read causal: %w", err)
	}
	return resp.CollectionIds, nil
}

// certTXs returns the transactions associated to the given collections.
func (n *primaryClient) certTXs(ctx context.Context, collDigests ...*narwhalproto.CertificateDigest) (types.Txs, error) {
	if len(collDigests) == 0 {
		return nil, nil
	}

	resp, err := n.vc.GetCollections(ctx, &narwhalproto.GetCollectionsRequest{
		CollectionIds: collDigests,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	batches, err := takeBatchesFromCollectionsResult(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to take batches from collection results: %w", err)
	}

	var out types.Txs
	for _, batchMsg := range batches {
		if batchMsg.Transactions == nil {
			continue
		}
		for _, tx := range batchMsg.Transactions.Transaction {
			out = append(out, tx.Transaction)
		}
	}
	return out, nil
}

// RemoveCollections removes collections from existence. This is run after consensus as the
// collections are no longer needed.
func (n *primaryClient) RemoveCollections(ctx context.Context, collDigests ...CertificateDigest) error {
	_, err := n.vc.RemoveCollections(ctx, &narwhalproto.RemoveCollectionsRequest{
		CollectionIds: mapSlice(collDigests, certDigestToProtoCertDigest),
	})
	if err != nil {
		return fmt.Errorf("failed remove collections: %w", err)
	}
	return nil
}

// NextBlockCerts provides the collection ID of the starting collection ID to be used with readCause while
// also returning the additional collection IDs that can be added to the block from later rounds that may be sibling
// and/or not included from the BFS from the starting collection ID via ReadCausal.
func (n *primaryClient) NextBlockCerts(ctx context.Context, opts mempool.ReapOption) (DAGCerts, error) {
	causalCollection, collections, err := nextBlockCollections(ctx, nextBlockIn{
		opts:      opts,
		publicKey: n.publicKey,
		pc:        n.pc,
		vc:        n.vc,
	})
	if err != nil {
		return DAGCerts{}, fmt.Errorf("failed to get next block collections: %w", err)
	}

	return DAGCerts{
		RootCert:   protoCertDigestToCertDigest(causalCollection),
		ExtraCerts: mapSlice(collections, protoCertDigestToCertDigest),
		client:     n,
	}, nil
}

type nextBlockIn struct {
	opts      mempool.ReapOption
	publicKey []byte

	pc narwhalproto.ProposerClient
	vc narwhalproto.ValidatorClient
}

func nextBlockCollections(ctx context.Context, in nextBlockIn) (causalCollection *narwhalproto.CertificateDigest, collections []*narwhalproto.CertificateDigest, _ error) {
	prep := &blockPrep{
		blockGasLimit: in.opts.BlockSizeLimit,
		blockTXsLimit: int64(in.opts.NumTxs),
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
	blockGasLimit int64
	blockTXsLimit int64
	publicKey     narwhalproto.PublicKey

	pc narwhalproto.ProposerClient
	vc narwhalproto.ValidatorClient

	mSeenCollections map[string]bool
}

func (b *blockPrep) nextBlockCollections(ctx context.Context) (*narwhalproto.CertificateDigest, []*narwhalproto.CertificateDigest, error) {
	roundsResp, err := b.pc.Rounds(ctx, &narwhalproto.RoundsRequest{PublicKey: &b.publicKey})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to make rounds request for pk(%s): %w", b.publicKey.Bytes, err)
	}

	oldest, mostRecentRound := roundsResp.OldestRound, roundsResp.NewestRound
	currentRound := oldest + 1
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
		if mColls[string(extraCollections[i].Digest)] {
			continue
		}
		mColls[string(extraCollections[i].Digest)] = true
		dedupedExtraColls = append(dedupedExtraColls, extraCollections[i])
	}

	return rootCollection, roundCollections, nil
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
	return b.traverseRounds(
		ctx,
		currentRound, mostRecentRound,
		0, 0,
		nil,
	)
}

func (b *blockPrep) traverseRounds(
	ctx context.Context,
	currentRound, mostRecentRound uint64,
	proposedBlockGasCost, proposedBlockNumTXs int64,
	collections []*narwhalproto.CertificateDigest,
) (lastCompletedRound uint64, extraCollections []*narwhalproto.CertificateDigest, _ error) {
	readCausalResp, err := b.pc.NodeReadCausal(ctx, &narwhalproto.NodeReadCausalRequest{
		PublicKey: &b.publicKey,
		Round:     currentRound,
	})
	if err != nil {
		if strings.Contains(err.Error(), "No known certificates for this authority") {
			return currentRound - 1, collections, nil
		}
		return 0, nil, fmt.Errorf("failed node read cause for current round %d (most recent rount %d): %w", currentRound, mostRecentRound, err)
	}

	colls, duplicatesRemoved := b.getNewCollectionIDs(readCausalResp.CollectionIds)

	var newColls []*narwhalproto.CertificateDigest
	for _, collection := range colls {
		batches, err := b.getCollectionBatches(ctx, collection)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get collection batches: %w", err)
		}

		stats := b.getBatchesStats(batches...)
		proposedBlockGasCost += stats.sizeBytes
		proposedBlockNumTXs += stats.numTXs
		if b.gasExceeded(proposedBlockGasCost) || b.numTXsExceeded(proposedBlockNumTXs) {
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
		return currentRound - 1, newColls, nil
		// if we are one round ahead of the newest round, then we take the extra collections again
		// and set the lastCompletedRound to most recent round. This allows us to take nodes in the DAG
		// that are at the same height as the newest round (the new colls).
	case currentRound == mostRecentRound+1:
		return mostRecentRound, newColls, nil
	}

	// recurse further until we run out of rounds or exceed the gas threshold
	return b.traverseRounds(ctx, currentRound+1, mostRecentRound, proposedBlockGasCost, proposedBlockNumTXs, newColls)
}

func (b *blockPrep) getBatchesStats(batches ...*narwhalproto.BatchMessage) (out struct{ numTXs, sizeBytes int64 }) {
	for _, batch := range batches {
		if batch.Transactions == nil {
			continue
		}
		for _, tx := range batch.Transactions.Transaction {
			out.numTXs++
			out.sizeBytes += int64(len(tx.Transaction))
		}
	}
	return out
}

func (b *blockPrep) gasExceeded(proposedGas int64) bool {
	return limitExceeded(b.blockGasLimit, proposedGas)
}

func (b *blockPrep) numTXsExceeded(proposedNumTXs int64) bool {
	return limitExceeded(int64(b.blockTXsLimit), proposedNumTXs)
}

func (b *blockPrep) getCollectionBatches(ctx context.Context, collections ...*narwhalproto.CertificateDigest) ([]*narwhalproto.BatchMessage, error) {
	collectionResp, err := b.vc.GetCollections(ctx, &narwhalproto.GetCollectionsRequest{
		CollectionIds: collections,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get collection IDs: %w", err)
	}

	batches, err := takeBatchesFromCollectionsResult(collectionResp)
	if err != nil {
		return nil, fmt.Errorf("failed to map collection results: %w", err)
	}

	return batches, nil
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

func takeBatchesFromCollectionsResult(resp *narwhalproto.GetCollectionsResponse) ([]*narwhalproto.BatchMessage, error) {
	var (
		errs    []error
		batches []*narwhalproto.BatchMessage
	)
	for i, res := range resp.Result {
		batch, resErr := res.GetBatch(), res.GetError()
		if resErr != nil {
			errs = append(errs, fmt.Errorf("failed collection result for id(%s) idx(%d): %s", base64Encode(resErr.Id.Digest), i, resErr.Error.String()))
			continue
		}
		batches = append(batches, batch)
	}
	return batches, multierror.Append(nil, errs...).ErrorOrNil()
}

func limitExceeded(limit, actual int64) bool {
	return limit > 0 && limit < actual
}
