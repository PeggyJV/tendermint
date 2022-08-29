package consensus

import (
	"context"

	abci "github.com/tendermint/tendermint/abci/types"
	mempl "github.com/tendermint/tendermint/mempool"
	tmstate "github.com/tendermint/tendermint/proto/tendermint/state"
	"github.com/tendermint/tendermint/proxy"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
)

//-----------------------------------------------------------------------------

type emptyMempool struct{}

var (
	_ sm.Mempool = emptyMempool{}
	_ TxNotifier = emptyMempool{}
)

func (e emptyMempool) AfterBlockFinality(ctx context.Context, block *types.Block, txResults []*abci.ResponseDeliverTx, newPreFn mempl.PreCheckFunc, newPostFn mempl.PostCheckFunc) error {
	return nil
}

func (e emptyMempool) PrepBlockFinality(_ context.Context) (func(), error) {
	return func() {}, nil
}

func (e emptyMempool) Reap(ctx context.Context, opts ...mempl.ReapOptFn) (mempl.ReapResults, error) {
	return mempl.ReapResults{}, nil
}

func (e emptyMempool) TxsAvailable() <-chan struct{} {
	return nil
}

//-----------------------------------------------------------------------------
// mockProxyApp uses ABCIResponses to give the right results.
//
// Useful because we don't want to call Commit() twice for the same block on
// the real app.

func newMockProxyApp(appHash []byte, abciResponses *tmstate.ABCIResponses) proxy.AppConnConsensus {
	clientCreator := proxy.NewLocalClientCreator(&mockProxyApp{
		appHash:       appHash,
		abciResponses: abciResponses,
	})
	cli, _ := clientCreator.NewABCIClient()
	err := cli.Start()
	if err != nil {
		panic(err)
	}
	return proxy.NewAppConnConsensus(cli)
}

type mockProxyApp struct {
	abci.BaseApplication

	appHash       []byte
	txCount       int
	abciResponses *tmstate.ABCIResponses
}

func (mock *mockProxyApp) DeliverTx(req abci.RequestDeliverTx) abci.ResponseDeliverTx {
	r := mock.abciResponses.DeliverTxs[mock.txCount]
	mock.txCount++
	if r == nil {
		return abci.ResponseDeliverTx{}
	}
	return *r
}

func (mock *mockProxyApp) EndBlock(req abci.RequestEndBlock) abci.ResponseEndBlock {
	mock.txCount = 0
	return *mock.abciResponses.EndBlock
}

func (mock *mockProxyApp) Commit() abci.ResponseCommit {
	return abci.ResponseCommit{Data: mock.appHash}
}
