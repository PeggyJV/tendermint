package narwhalc

import (
	"context"
	"fmt"

	"github.com/tendermint/tendermint/mempool/narwhalc/narwhalproto"
	"github.com/tendermint/tendermint/types"
)

// workerClient is the grpc Transactions service client with additional
// cluster information for debugging/error handling.
type workerClient struct {
	tc narwhalproto.TransactionsClient
	clientBase
}

// newWorkerClient creates a new narwhal worker node client.
func newWorkerClient(ctx context.Context, nodeEncodedPubKey, addr, label string) (*workerClient, error) {
	cc, err := newGRPCConnection(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection for worker at addr(%s): %w", addr, err)
	}

	return &workerClient{
		tc: narwhalproto.NewTransactionsClient(cc),
		clientBase: clientBase{
			meta: NodeMeta{
				Name:  nodeEncodedPubKey,
				Type:  "worker",
				Label: label,
				Addr:  addr,
			},
		},
	}, nil
}

// SubmitTransaction submits a single transaction.
func (t *workerClient) SubmitTransaction(ctx context.Context, b types.Tx) error {
	_, err := t.tc.SubmitTransaction(ctx, &narwhalproto.Transaction{Transaction: b})
	if err != nil {
		return fmt.Errorf("failed to submit TX: %w", err)
	}
	return nil
}
