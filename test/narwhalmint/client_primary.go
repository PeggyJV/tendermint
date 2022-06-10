package narwhalmint

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/tendermint/tendermint/test/narwhalmint/narwhal"
	"google.golang.org/grpc"
)

// NarwhalPrimaryNodeClient is the grpc Validator service client with additional
// cluster information for debugging/error handling.
type NarwhalPrimaryNodeClient struct {
	blockSizeLimit int
	publicKey      []byte

	vc narwhal.ValidatorClient
	pc narwhal.ProposerClient
	clientBase
}

func newNarwhalPrimaryNodeClient(cc *grpc.ClientConn, nodeName, addr string, blockSizeLimit int) (*NarwhalPrimaryNodeClient, error) {
	publicKey, err := base64.StdEncoding.DecodeString(nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to base64 decode public key from node name(%s): %w", nodeName, err)
	}
	return &NarwhalPrimaryNodeClient{
		vc:             narwhal.NewValidatorClient(cc),
		pc:             narwhal.NewProposerClient(cc),
		blockSizeLimit: blockSizeLimit,
		publicKey:      publicKey,
		clientBase: clientBase{
			meta: NodeMeta{
				NodeName: nodeName,
				Addr:     addr,
			},
		},
	}, nil
}

// CollectionsFrom returns a list of transactions starting from the provided argument and walking the DAG
// in BFS fashion to obtain all child collections.
func (n *NarwhalPrimaryNodeClient) CollectionsFrom(ctx context.Context, startingCollection *narwhal.CertificateDigest) ([]*narwhal.CertificateDigest, error) {
	resp, err := n.vc.ReadCausal(ctx, &narwhal.ReadCausalRequest{
		CollectionId: startingCollection,
	})
	if err != nil {
		return nil, fmt.Errorf("failed read causal: %w", err)
	}
	return resp.CollectionIds, nil
}

// CollectionTXs returns the transactions associated to the given collections.
func (n *NarwhalPrimaryNodeClient) CollectionTXs(ctx context.Context, collectionIDs ...*narwhal.CertificateDigest) ([][]byte, error) {
	resp, err := n.vc.GetCollections(ctx, &narwhal.GetCollectionsRequest{
		CollectionIds: collectionIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	batches, err := takeBatchesFromCollectionsResult(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to take batches from collection results: %w", err)
	}

	var out [][]byte
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

// RemoveCollection removes collections from existence. This is run after consensus as the collections are no longer
// needed.
func (n *NarwhalPrimaryNodeClient) RemoveCollections(ctx context.Context, collections ...*narwhal.CertificateDigest) error {
	_, err := n.vc.RemoveCollections(ctx, &narwhal.RemoveCollectionsRequest{
		CollectionIds: collections,
	})
	if err != nil {
		return fmt.Errorf("failed remove collections: %w", err)
	}
	return nil
}

// NextBlockCollections provides the collection ID of the starting collection ID to be used with readCause while
// also returning the additional collection IDs that can be added to the block from later rounds that may be sibling
// and/or not included from the BFS from the starting collection ID via ReadCausal.
func (n *NarwhalPrimaryNodeClient) NextBlockCollections(ctx context.Context) ([]*narwhal.CertificateDigest, error) {
	collections, err := nextBlockCollections(ctx, nextBlockIn{
		blockSizeLimit: n.blockSizeLimit,
		publicKey:      n.publicKey,
		pc:             n.pc,
		vc:             n.vc,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get next block collections: %w", err)
	}
	return collections, nil
}

type nextBlockIn struct {
	blockSizeLimit int
	publicKey      []byte

	pc narwhal.ProposerClient
	vc narwhal.ValidatorClient
}

func nextBlockCollections(ctx context.Context, in nextBlockIn) (collections []*narwhal.CertificateDigest, _ error) {
	prep := &blockPrep{
		blockGasLimit: in.blockSizeLimit,
		publicKey: narwhal.PublicKey{
			Bytes: in.publicKey,
		},
		pc:               in.pc,
		vc:               in.vc,
		mSeenCollections: make(map[string]bool),
	}
	return prep.nextBlockCollections(ctx)
}

type blockPrep struct {
	blockGasLimit int
	publicKey     narwhal.PublicKey

	pc narwhal.ProposerClient
	vc narwhal.ValidatorClient

	mSeenCollections map[string]bool
}

func (b *blockPrep) nextBlockCollections(ctx context.Context) ([]*narwhal.CertificateDigest, error) {
	roundsResp, err := b.pc.Rounds(ctx, &narwhal.RoundsRequest{PublicKey: &b.publicKey})
	if err != nil {
		return nil, fmt.Errorf("failed to make rounds request for pk(%s): %w", b.publicKey.Bytes, err)
	}

	oldest, mostRecent := roundsResp.OldestRound, roundsResp.NewestRound
	currentRound := oldest + 1
	lastCompletedRound, extraCollections, err := b.getRelevantBlockRound(ctx, currentRound, mostRecent, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get relevant block round (start=%d, oldest=%d): %w", oldest, mostRecent, err)
	}

	collections, err := b.findRoundCollections(ctx, oldest, lastCompletedRound)
	if err != nil {
		return nil, fmt.Errorf("failed to find starting collection ID: %w", err)
	}

	mColls := make(map[string]bool, len(collections))
	for i := range collections {
		mColls[string(collections[i].Digest)] = true
	}
	for i := range extraCollections {
		if mColls[string(extraCollections[i].Digest)] {
			continue
		}
		mColls[string(extraCollections[i].Digest)] = true
		collections = append(collections, extraCollections[i])
	}

	return collections, nil
}

func (b *blockPrep) findRoundCollections(ctx context.Context, startRound, currentRound uint64) ([]*narwhal.CertificateDigest, error) {
	if currentRound < startRound {
		return nil, fmt.Errorf("failed to find valid round via node read causal")
	}

	nrcResp, err := b.pc.NodeReadCausal(ctx, &narwhal.NodeReadCausalRequest{
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

func (b *blockPrep) getRelevantBlockRound(ctx context.Context, currentRound, mostRecentRound uint64, proposedBlockGasCost int, collections []*narwhal.CertificateDigest) (lastCompletedRound uint64, extraCollections []*narwhal.CertificateDigest, _ error) {
	readCausalResp, err := b.pc.NodeReadCausal(ctx, &narwhal.NodeReadCausalRequest{
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

	var newColls []*narwhal.CertificateDigest
	for _, collection := range colls {
		batches, err := b.getCollectionBatches(ctx, collection)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get collection batches: %w", err)
		}

		cost, err := b.getBatchesSize(batches...)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get batch gas cost: %w", err)
		}
		proposedBlockGasCost += cost
		if proposedBlockGasCost > b.blockGasLimit {
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
	return b.getRelevantBlockRound(ctx, currentRound+1, mostRecentRound, proposedBlockGasCost, newColls)
}

func (b *blockPrep) getBatchesSize(batches ...*narwhal.BatchMessage) (int, error) {
	var size int
	for _, batch := range batches {
		if batch.Transactions == nil {
			continue
		}
		for _, tx := range batch.Transactions.Transaction {
			size += len(tx.Transaction)
		}
	}
	return size, nil
}

func (b *blockPrep) getCollectionBatches(ctx context.Context, collections ...*narwhal.CertificateDigest) ([]*narwhal.BatchMessage, error) {
	collectionResp, err := b.vc.GetCollections(ctx, &narwhal.GetCollectionsRequest{
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

func (b *blockPrep) getNewCollectionIDs(collectionIDs []*narwhal.CertificateDigest) (_ []*narwhal.CertificateDigest, numDuplicatesRemoved int) {
	var out []*narwhal.CertificateDigest
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

func takeBatchesFromCollectionsResult(resp *narwhal.GetCollectionsResponse) ([]*narwhal.BatchMessage, error) {
	var (
		errs    []error
		batches []*narwhal.BatchMessage
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

func base64Encode(src []byte) string {
	return base64.StdEncoding.EncodeToString(src)
}
