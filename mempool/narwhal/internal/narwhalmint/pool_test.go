//go:build narwhal

package narwhalmint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/abci/example/kvstore"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/mempool/narwhal"
	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalmint"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

const (
	kilobyte = 1 << 10
)

func TestNarwhalPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)

	logger := log.TestingLogger()

	abciC, err := cc.NewABCIClient()
	require.NoError(t, err)
	abciC.SetLogger(logger)
	require.NoError(t, abciC.Start())

	l := narwhalmint.LauncherNarwhal{
		Host:      "127.0.0.1",
		Primaries: 4,
		Workers:   1,
		Out:       os.Stdout,
	}

	t.Log("setting up filesystem...")
	require.NoError(t, l.SetupFS(ctx, time.Now()))

	t.Log("starting narwhal nodes...")
	require.NoError(t, l.Start(ctx))

	wait(t, 5, "narwhal nodes to startup...")

	runner := newReapTracker(t)

	memLogger := logger.With("module", "mempool")
	mempoolCFG := config.TestMempoolConfig()

	primaryCFGs := l.NarwhalMempoolConfigs()
	mpABCIs := make([]*mempool.ABCI, 0, len(primaryCFGs))
	for _, narwhalCFG := range primaryCFGs {
		pool, err := narwhal.New(ctx, narwhalCFG, narwhal.WithLogger(memLogger.With("component", "narwhal")))
		require.NoError(t, err)

		mpABCIs = append(mpABCIs, mempool.NewABCI(
			memLogger.With("component", "abci"),
			mempoolCFG,
			abciC,
			pool,
		))
	}

	multiplier := 2000

	var wg sync.WaitGroup
	for i := range mpABCIs {
		wg.Add(1)
		go func(i int, mp *mempool.ABCI) {
			defer wg.Done()
			start := i * multiplier
			finish := start + (multiplier - 1)
			runner.checkTXRange(ctx, mp, start, finish, uint16(i))
		}(i, mpABCIs[i])
	}
	wg.Wait()

	wait(t, 5, "narwhal round to complete\n\n")

	maxBytes := int64(150) * kilobyte
	for i := range mpABCIs {
		t.Logf("reaping mp[%d]", i)
		runner.reapTxs(ctx, mpABCIs[i], mempool.ReapBytes(maxBytes))
	}
	consensusBlock := runner.reapTxs(ctx, mpABCIs[0], mempool.ReapBytes(maxBytes))

	finishFn, err := mpABCIs[0].PrepBlockFinality(ctx)
	require.NoError(t, err)
	defer finishFn()

	err = mpABCIs[0].AfterBlockFinality(ctx, consensusBlock, nil, nil, nil)
	require.NoError(t, err)

	cancel()
	for err := range l.NodeRuntimeErrs() {
		t.Log("runtime err: ", err)
	}
}

type reapTracker struct {
	t *testing.T

	mu          sync.Mutex
	mTxsChecked map[string]struct{}
	mTxsReaped  map[string]struct{}
}

func newReapTracker(t *testing.T) *reapTracker {
	return &reapTracker{
		t:           t,
		mTxsChecked: make(map[string]struct{}),
		mTxsReaped:  make(map[string]struct{}),
	}
}

func (r *reapTracker) checkTXRange(ctx context.Context, mp *mempool.ABCI, start, finish int, peerID uint16) types.Txs {
	r.t.Helper()

	txs := checkTxs(ctx, r.t, mp, start, finish, peerID)
	r.mu.Lock()
	for _, tx := range txs {
		r.mTxsChecked[string(tx)] = struct{}{}
	}
	r.mu.Unlock()
	return txs
}

func (r *reapTracker) reapTxs(ctx context.Context, mp *mempool.ABCI, opts ...mempool.ReapOptFn) *types.Block {
	t := r.t
	t.Helper()

	opt := mempool.CoalesceReapOpts(opts...)

	reapRes, err := mp.Reap(ctx, opts...)
	require.NoError(t, err)

	collections := reapRes.Collections
	require.NotNil(t, collections)
	t.Logf("collections: %+v", collections)

	consensusBlock := &types.Block{Header: types.Header{Collections: collections}}

	txs := reapRes.Txs
	if len(txs) == 0 {
		t.Log("reaped 0 txs")
		return consensusBlock
	}

	r.mu.Lock()
	for _, tx := range txs {
		r.mTxsReaped[string(tx)] = struct{}{}
	}
	r.mu.Unlock()

	stats := r.txStats()
	t.Logf("reap stats:\n\t\tblock_size_limit=%d\tgas_limit=%d\tmax_txs=%d\n\t\ttxs_in_current_reap=%d\ttxs_checked=%d\ttxs_reaped=%d\n\t\tchecked_size=%d\treaped_size=%d\n\t\toldest_unreaped=%s newest_unreaped=%s\n\n",
		opt.BlockSizeLimit, opt.GasLimit, opt.NumTxs,
		len(txs), stats.NumTxsChecked, stats.NumTxsReaped,
		stats.SizeTxsChecked, stats.SizeTxsReaped,
		stats.OldestUnreaped(), stats.NewestUnreaped(),
	)

	return consensusBlock
}

func (r *reapTracker) txStats() txStats {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.t.Helper()

	var checkSize, reapSize int
	mChecked := make(map[string]struct{}, len(r.mTxsChecked))
	for k := range r.mTxsChecked {
		mChecked[k] = struct{}{}
		checkSize += len([]byte(k))
	}
	for k := range r.mTxsReaped {
		delete(mChecked, k)
		reapSize += len([]byte(k))
	}

	txs := make([]string, 0, len(mChecked))
	for k := range mChecked {
		txs = append(txs, k)
	}
	sort.Slice(txs, func(i, j int) bool {
		var tx1, tx2 struct {
			ID int `json:"tx_id"`
		}
		require.NoError(r.t, json.Unmarshal([]byte(txs[i]), &tx1))
		require.NoError(r.t, json.Unmarshal([]byte(txs[j]), &tx2))
		return tx1.ID < tx2.ID
	})

	return txStats{
		NumTxsChecked:  len(r.mTxsChecked),
		NumTxsReaped:   len(r.mTxsReaped),
		SizeTxsChecked: checkSize,
		SizeTxsReaped:  reapSize,
		UnreapedTxs:    txs,
	}
}

type txStats struct {
	NumTxsChecked  int
	NumTxsReaped   int
	SizeTxsChecked int
	SizeTxsReaped  int
	UnreapedTxs    []string
}

func (t txStats) OldestUnreaped() string {
	return t.getUnreapedTx(0)
}

func (t txStats) NewestUnreaped() string {
	return t.getUnreapedTx(len(t.UnreapedTxs) - 1)
}

func (t txStats) getUnreapedTx(i int) string {
	if len(t.UnreapedTxs) == 0 {
		return ""
	}
	return t.UnreapedTxs[i]
}

func wait(t testing.TB, secs int, details string) {
	t.Helper()
	dur := time.Duration(secs) * time.Second
	t.Logf("waiting %s for %s", dur.String(), details)
	time.Sleep(dur)
}

func checkTxs(ctx context.Context, t *testing.T, mp *mempool.ABCI, start, finish int, peerID uint16) types.Txs {
	t.Helper()

	var txs types.Txs
	txInfo := mempool.TxInfo{SenderID: peerID}
	for _, tx := range newRangedTxs(start, finish) {
		err := mp.CheckTx(ctx, tx, nil, txInfo)
		require.NoErrorf(t, err, "CheckTx failed: %v while checking %s", err, string(tx))
		txs = append(txs, tx)
	}
	return txs
}

func newRangedTxs(start, finish int) types.Txs {
	var out types.Txs
	for i := start; i <= finish; i++ {
		tx := []byte(fmt.Sprintf(`{"tx_id": %[1]d, "data": "data-%[1]d"}`, i))
		out = append(out, tx)
	}
	return out
}
