package narwhalc

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"sync"

	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalproto"
	"github.com/tendermint/tendermint/types"
)

// WorkerClient is the grpc Transactions service client with additional
// cluster information for debugging/error handling.
type WorkerClient struct {
	mu       sync.Mutex
	done     <-chan struct{}
	tc       narwhalproto.TransactionsClient
	txStream narwhalproto.Transactions_SubmitTransactionStreamClient
	clientBase
}

// NewWorkerClient creates a new narwhal worker node client.
func NewWorkerClient(ctx context.Context, addr, name string) (*WorkerClient, error) {
	cc, err := newGRPCConnection(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection for worker at addr(%s): %w", addr, err)
	}

	return &WorkerClient{
		tc:   narwhalproto.NewTransactionsClient(cc),
		done: ctx.Done(),
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
func (w *WorkerClient) SubmitTransaction(ctx context.Context, tx types.Tx, gasWanted int64) error {
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

	txStream, err := w.getTxStream(ctx)
	if err != nil {
		return err
	}

	err = txStream.Send(&narwhalproto.Transaction{Transaction: buf.Bytes()})
	if err != nil {
		w.mu.Lock()
		{
			// reset txStream on error
			w.txStream = nil
		}
		w.mu.Unlock()
		return err
	}

	return nil
}

func (w *WorkerClient) getTxStream(ctx context.Context) (narwhalproto.Transactions_SubmitTransactionStreamClient, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.txStream == nil {
		// we create the txStream lazily to avoid startup retries that may or may not resolve
		// leaving us in the same place we are now, creating the stream upon submit of a tx.
		txStream, err := w.tc.SubmitTransactionStream(ctx)
		if err != nil {
			return nil, err
		}
		w.txStream = txStream
	}

	return w.txStream, nil
}
