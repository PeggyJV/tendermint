package narwhalc

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalproto"
	"github.com/tendermint/tendermint/types"
)

// WorkerClient is the grpc Transactions service client with additional
// cluster information for debugging/error handling.
type WorkerClient struct {
	tc narwhalproto.TransactionsClient
	clientBase
}

// NewWorkerClient creates a new narwhal worker node client.
func NewWorkerClient(ctx context.Context, addr, name string) (*WorkerClient, error) {
	cc, err := newGRPCConnection(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection for worker at addr(%s): %w", addr, err)
	}

	return &WorkerClient{
		tc: narwhalproto.NewTransactionsClient(cc),
		clientBase: clientBase{
			meta: NodeMeta{
				Name: name,
				Type: "worker",
				Addr: addr,
			},
		},
	}, nil
}

// SubmitTransaction submits a single transaction.
func (t *WorkerClient) SubmitTransaction(ctx context.Context, tx types.Tx, gasWanted int64) error {
	var buf bytes.Buffer
	err := gob.
		NewEncoder(&buf).
		Encode(txFull{
			GasWanted: gasWanted,
			Tx:        tx,
		})
	if err != nil {
		return err
	}
	_, err = t.tc.SubmitTransaction(ctx, &narwhalproto.Transaction{Transaction: buf.Bytes()})
	return err
}
