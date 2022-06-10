package narwhalmint

import (
	"context"
	"fmt"

	"github.com/tendermint/tendermint/test/narwhalmint/narwhal"
	"google.golang.org/grpc"
)

// NarwhalWorkerNodeClient is the grpc Transactions service client with additional
// cluster information for debugging/error handling.
type NarwhalWorkerNodeClient struct {
	tc narwhal.TransactionsClient
	clientBase
}

func newNarwhalNodeClient(cc *grpc.ClientConn, nodeName, workerID, addr string) *NarwhalWorkerNodeClient {
	return &NarwhalWorkerNodeClient{
		tc: narwhal.NewTransactionsClient(cc),
		clientBase: clientBase{
			meta: NodeMeta{
				NodeName: nodeName,
				WorkerID: workerID,
				Addr:     addr,
			},
		},
	}
}

// SubmitTransaction submits a single transaction.
func (t *NarwhalWorkerNodeClient) SubmitTransaction(ctx context.Context, b []byte) error {
	_, err := t.tc.SubmitTransaction(ctx, &narwhal.Transaction{Transaction: b})
	if err != nil {
		return fmt.Errorf("failed to submit TX: %w", err)
	}
	return nil
}
