package narwhalc

import (
	"context"

	"google.golang.org/grpc"

	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalproto"
	"github.com/tendermint/tendermint/observe"
)

type mwProposer struct {
	next narwhalproto.ProposerClient

	nodeReadCausalRED observe.RED
	roundsRED         observe.RED
}

func proposerMetrics(namespace string, labelsAndVals ...string) func(proposer narwhalproto.ProposerClient) narwhalproto.ProposerClient {
	redGen := newREDGen(namespace, "narwhal_proposerc", labelsAndVals...)
	return func(proposer narwhalproto.ProposerClient) narwhalproto.ProposerClient {
		return &mwProposer{
			next: proposer,

			nodeReadCausalRED: redGen("node_read_causal"),
			roundsRED:         redGen("rounds"),
		}
	}
}

func (m *mwProposer) Rounds(ctx context.Context, in *narwhalproto.RoundsRequest, opts ...grpc.CallOption) (*narwhalproto.RoundsResponse, error) {
	record := m.roundsRED.Incr()
	resp, err := m.next.Rounds(ctx, in, opts...)
	return resp, record(err)
}

func (m *mwProposer) NodeReadCausal(ctx context.Context, in *narwhalproto.NodeReadCausalRequest, opts ...grpc.CallOption) (*narwhalproto.NodeReadCausalResponse, error) {
	record := m.nodeReadCausalRED.Incr()
	resp, err := m.next.NodeReadCausal(ctx, in, opts...)
	return resp, record(err)
}

type mwValidator struct {
	next narwhalproto.ValidatorClient

	getCollRED    observe.RED
	readCausalRED observe.RED
	rmCollRED     observe.RED
}

func validatorMetrics(namespace string, labelsAndVals ...string) func(client narwhalproto.ValidatorClient) narwhalproto.ValidatorClient {
	redGen := newREDGen(namespace, "narwhal_validatorc", labelsAndVals...)
	return func(client narwhalproto.ValidatorClient) narwhalproto.ValidatorClient {
		return &mwValidator{
			next: client,

			getCollRED:    redGen("get_collections"),
			readCausalRED: redGen("read_causal"),
			rmCollRED:     redGen("remove_collections"),
		}
	}
}

func (m *mwValidator) GetCollections(ctx context.Context, in *narwhalproto.GetCollectionsRequest, opts ...grpc.CallOption) (*narwhalproto.GetCollectionsResponse, error) {
	record := m.getCollRED.Incr()
	resp, err := m.next.GetCollections(ctx, in, opts...)
	return resp, record(err)
}

func (m *mwValidator) RemoveCollections(ctx context.Context, in *narwhalproto.RemoveCollectionsRequest, opts ...grpc.CallOption) (*narwhalproto.Empty, error) {
	record := m.rmCollRED.Incr()
	resp, err := m.next.RemoveCollections(ctx, in, opts...)
	return resp, record(err)
}

func (m *mwValidator) ReadCausal(ctx context.Context, in *narwhalproto.ReadCausalRequest, opts ...grpc.CallOption) (*narwhalproto.ReadCausalResponse, error) {
	record := m.readCausalRED.Incr()
	resp, err := m.next.ReadCausal(ctx, in, opts...)
	return resp, record(err)
}

type mwTxs struct {
	next narwhalproto.TransactionsClient

	sendRED     observe.RED
	submitRED   observe.RED
	txStreamRED observe.RED
}

func txsMetrics(namespace string, labelsAndVals ...string) func(client narwhalproto.TransactionsClient) narwhalproto.TransactionsClient {
	redGen := newREDGen(namespace, "narwhal_txsc", labelsAndVals...)
	return func(client narwhalproto.TransactionsClient) narwhalproto.TransactionsClient {
		return &mwTxs{
			next: client,

			sendRED:     redGen("stream_send_tx"),
			submitRED:   redGen("submit_tx"),
			txStreamRED: redGen("new_tx_stream"),
		}
	}
}

func (m *mwTxs) SubmitTransaction(ctx context.Context, in *narwhalproto.Transaction, opts ...grpc.CallOption) (*narwhalproto.Empty, error) {
	record := m.submitRED.Incr()
	resp, err := m.next.SubmitTransaction(ctx, in, opts...)
	return resp, record(err)
}

func (m *mwTxs) SubmitTransactionStream(ctx context.Context, opts ...grpc.CallOption) (narwhalproto.Transactions_SubmitTransactionStreamClient, error) {
	record := m.txStreamRED.Incr()
	resp, err := m.next.SubmitTransactionStream(ctx, opts...)
	return txStreamMetrics(m.sendRED, resp), record(err)
}

type mwSend struct {
	narwhalproto.Transactions_SubmitTransactionStreamClient

	sendRED observe.RED
}

func txStreamMetrics(red observe.RED, next narwhalproto.Transactions_SubmitTransactionStreamClient) narwhalproto.Transactions_SubmitTransactionStreamClient {
	return &mwSend{
		Transactions_SubmitTransactionStreamClient: next,
		sendRED: red,
	}
}

func (m *mwSend) Send(tx *narwhalproto.Transaction) error {
	record := m.sendRED.Incr()
	return record(m.Transactions_SubmitTransactionStreamClient.Send(tx))
}

func newREDGen(namespace, subsystem string, labelVals ...string) func(string) observe.RED {
	return func(opName string) observe.RED {
		return observe.NewRED(namespace, subsystem, opName, labelVals...)
	}
}
