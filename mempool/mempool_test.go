package mempool

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	mrand "math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	gogotypes "github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/abci/example/counter"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	abciserver "github.com/tendermint/tendermint/abci/server"
	abci "github.com/tendermint/tendermint/abci/types"
	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
)

// A cleanupFunc cleans up any config / test files created for a particular
// test.
type cleanupFunc func()

func newMempoolWithApp(cc proxy.ClientCreator) (*PoolCList, *ABCI, cleanupFunc) {
	return newMempoolWithAppAndConfig(cc, cfg.ResetTestRoot("mempool_test"))
}

func newMempoolWithAppAndConfig(cc proxy.ClientCreator, config *cfg.Config) (*PoolCList, *ABCI, cleanupFunc) {
	appConnMem, _ := cc.NewABCIClient()
	appConnMem.SetLogger(log.TestingLogger().With("module", "abci-client", "connection", "mempool"))
	err := appConnMem.Start()
	if err != nil {
		panic(err)
	}
	clist := NewPoolCList(config.Mempool, 0)
	logger := log.TestingLogger()
	clist.SetLogger(logger.With("component", "clist_pool"))

	mp := NewABCI(
		logger.With("component", "mempool_abci"),
		config.Mempool,
		appConnMem,
		clist,
	)

	return clist, mp, func() {
		mp.Flush(context.TODO())
		os.RemoveAll(config.RootDir)
	}
}

func updateMempool(
	ctx context.Context,
	t *testing.T,
	mp *ABCI,
	height int64,
	txs types.Txs,
	resp []*abci.ResponseDeliverTx,
	preFn PreCheckFunc,
	postFn PostCheckFunc,
) {
	t.Helper()

	finishFn, err := mp.PrepBlockFinality(ctx)
	require.NoError(t, err)
	defer finishFn()

	block := types.Block{
		Header: types.Header{Height: height},
		Data:   types.Data{Txs: txs},
	}
	err = mp.AfterBlockFinality(ctx, &block, resp, preFn, postFn)
	require.NoError(t, err)
}

func ensureNoFire(t *testing.T, ch <-chan struct{}, timeoutMS int) {
	t.Helper()

	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("Expected not to fire")
	case <-timer.C:
	}
}

func ensureFire(t *testing.T, ch <-chan struct{}, timeoutMS int) {
	t.Helper()

	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	select {
	case <-ch:
	case <-timer.C:
		t.Fatal("Expected to fire")
	}
}

func checkTxs(t *testing.T, mp *ABCI, count int, peerID uint16) types.Txs {
	t.Helper()

	ctx := context.TODO()
	txs := make(types.Txs, count)
	txInfo := TxInfo{SenderID: peerID}
	for i := 0; i < count; i++ {
		txBytes := make([]byte, 20)
		txs[i] = txBytes
		_, err := rand.Read(txBytes)
		if err != nil {
			t.Error(err)
		}
		if err := mp.CheckTx(ctx, txBytes, nil, txInfo); err != nil {
			// Skip invalid txs.
			// TestMempoolFilters will fail otherwise. It asserts a number of txs
			// returned.
			if IsPreCheckError(err) {
				continue
			}
			t.Fatalf("CheckTx failed: %v while checking #%d tx", err, i)
		}
	}
	return txs
}

func TestReapMaxBytesMaxGas(t *testing.T) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	clist, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()

	// Ensure gas calculation behaves as expected
	checkTxs(t, mp, 1, UnknownPeerID)
	tx0 := clist.TxsFront().Value.(*mempoolTx)
	// assert that kv store has gas wanted = 1.
	require.Equal(t, app.CheckTx(abci.RequestCheckTx{Tx: tx0.tx}).GasWanted, int64(1), "KVStore had a gas value neq to 1")
	require.Equal(t, tx0.gasWanted, int64(1), "transactions gas was set incorrectly")
	// ensure each tx is 20 bytes long
	require.Equal(t, len(tx0.tx), 20, "Tx is longer than 20 bytes")
	require.NoError(t, mp.Flush(ctx))

	// each table driven test creates numTxsToCreate txs with checkTx, and at the end clears all remaining txs.
	// each tx has 20 bytes
	tests := []struct {
		numTxsToCreate int
		maxBytes       int64
		maxGas         int64
		expectedNumTxs int
	}{
		{20, -1, -1, 20},
		{20, -1, 0, 0},
		{20, -1, 10, 10},
		{20, -1, 30, 20},
		{20, 0, -1, 0},
		{20, 0, 10, 0},
		{20, 10, 10, 0},
		{20, 24, 10, 1},
		{20, 240, 5, 5},
		{20, 240, -1, 10},
		{20, 240, 10, 10},
		{20, 240, 15, 10},
		{20, 20000, -1, 20},
		{20, 20000, 5, 5},
		{20, 20000, 30, 20},
	}
	for tcIndex, tt := range tests {
		checkTxs(t, mp, tt.numTxsToCreate, UnknownPeerID)
		reaper, err := mp.Reap(ctx, ReapBytes(tt.maxBytes), ReapGas(tt.maxGas))
		require.NoError(t, err)

		got, err := reaper.Txs(ctx)
		require.NoError(t, err)
		assert.Equal(t, tt.expectedNumTxs, len(got), "Got %d txs, expected %d, tc #%d",
			len(got), tt.expectedNumTxs, tcIndex)
		require.NoError(t, mp.Flush(ctx))
	}
}

func TestMempoolFilters(t *testing.T) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()
	emptyTxArr := []types.Tx{[]byte{}}

	ctx := context.TODO()

	nopPreFilter := func(tx types.Tx) error { return nil }
	nopPostFilter := func(tx types.Tx, res *abci.ResponseCheckTx) error { return nil }

	// each table driven test creates numTxsToCreate txs with checkTx, and at the end clears all remaining txs.
	// each tx has 20 bytes
	tests := []struct {
		numTxsToCreate int
		preFilter      PreCheckFunc
		postFilter     PostCheckFunc
		expectedNumTxs int
	}{
		{10, nopPreFilter, nopPostFilter, 10},
		{10, PreCheckMaxBytes(10), nopPostFilter, 0},
		{10, PreCheckMaxBytes(22), nopPostFilter, 10},
		{10, nopPreFilter, PostCheckMaxGas(-1), 10},
		{10, nopPreFilter, PostCheckMaxGas(0), 0},
		{10, nopPreFilter, PostCheckMaxGas(1), 10},
		{10, nopPreFilter, PostCheckMaxGas(3000), 10},
		{10, PreCheckMaxBytes(10), PostCheckMaxGas(20), 0},
		{10, PreCheckMaxBytes(30), PostCheckMaxGas(20), 10},
		{10, PreCheckMaxBytes(22), PostCheckMaxGas(1), 10},
		{10, PreCheckMaxBytes(22), PostCheckMaxGas(0), 0},
	}
	for tcIndex, tt := range tests {
		updateMempool(ctx, t, mp, 1, emptyTxArr, abciResponses(len(emptyTxArr), abci.CodeTypeOK), tt.preFilter, tt.postFilter)
		checkTxs(t, mp, tt.numTxsToCreate, UnknownPeerID)
		require.Equal(t, tt.expectedNumTxs, mp.PoolMeta().Size, "mempool had the incorrect size, on test case %d", tcIndex)
		require.NoError(t, mp.Flush(ctx))
	}
}

func TestMempoolUpdate(t *testing.T) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()

	// 1. Adds valid txs to the cache
	{
		updateMempool(ctx, t, mp, 1, []types.Tx{[]byte{0x01}}, abciResponses(1, abci.CodeTypeOK), nil, nil)
	}

	// 2. Validate cache hit is rejected
	{
		err := mp.CheckTx(ctx, []byte{0x01}, nil, TxInfo{})
		if assert.Error(t, err) {
			assert.Equal(t, ErrTxInCache, err)
		}
	}

	// 3. Removes valid txs from the mempool
	{
		err := mp.CheckTx(ctx, []byte{0x02}, nil, TxInfo{})
		require.NoError(t, err)
		updateMempool(ctx, t, mp, 1, []types.Tx{[]byte{0x02}}, abciResponses(1, abci.CodeTypeOK), nil, nil)
		assert.Zero(t, mp.PoolMeta().Size)
	}

	// 3. Removes invalid transactions from the cache and the mempool (if present)
	{
		err := mp.CheckTx(ctx, []byte{0x03}, nil, TxInfo{})
		require.NoError(t, err)
		updateMempool(ctx, t, mp, 1, []types.Tx{[]byte{0x03}}, abciResponses(1, 1), nil, nil)
		assert.Zero(t, mp.PoolMeta().Size)

		err = mp.CheckTx(ctx, []byte{0x03}, nil, TxInfo{})
		require.NoError(t, err)
	}
}

func TestMempool_KeepInvalidTxsInCache(t *testing.T) {
	app := counter.NewApplication(true)
	cc := proxy.NewLocalClientCreator(app)
	wcfg := cfg.DefaultConfig()
	wcfg.Mempool.KeepInvalidTxsInCache = true
	_, mp, cleanup := newMempoolWithAppAndConfig(cc, wcfg)
	defer cleanup()

	ctx := context.TODO()

	// 1. An invalid transaction must remain in the cache after Update
	{
		a := make([]byte, 8)
		binary.BigEndian.PutUint64(a, 0)

		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, 1)

		err := mp.CheckTx(ctx, b, nil, TxInfo{})
		require.NoError(t, err)

		// simulate new block
		_ = app.DeliverTx(abci.RequestDeliverTx{Tx: a})
		_ = app.DeliverTx(abci.RequestDeliverTx{Tx: b})
		updateMempool(ctx, t, mp, 1, []types.Tx{a, b},
			[]*abci.ResponseDeliverTx{{Code: abci.CodeTypeOK}, {Code: 2}}, nil, nil)

		// a must be added to the cache
		err = mp.CheckTx(ctx, a, nil, TxInfo{})
		if assert.Error(t, err) {
			assert.Equal(t, ErrTxInCache, err)
		}

		// b must remain in the cache
		err = mp.CheckTx(ctx, b, nil, TxInfo{})
		if assert.Error(t, err) {
			assert.Equal(t, ErrTxInCache, err)
		}
	}

	// 2. An invalid transaction must remain in the cache
	{
		a := make([]byte, 8)
		binary.BigEndian.PutUint64(a, 0)

		// remove a from the cache to test (2)
		mp.cache.Remove(a)

		err := mp.CheckTx(ctx, a, nil, TxInfo{})
		require.NoError(t, err)

		err = mp.CheckTx(ctx, a, nil, TxInfo{})
		if assert.Error(t, err) {
			assert.Equal(t, ErrTxInCache, err)
		}
	}
}

func TestTxsAvailable(t *testing.T) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()

	mp.EnableTxsAvailable()

	timeoutMS := 500

	// with no txs, it shouldnt fire
	ensureNoFire(t, mp.TxsAvailable(), timeoutMS)

	// send a bunch of txs, it should only fire once
	txs := checkTxs(t, mp, 100, UnknownPeerID)
	ensureFire(t, mp.TxsAvailable(), timeoutMS)
	ensureNoFire(t, mp.TxsAvailable(), timeoutMS)

	// call update with half the txs.
	// it should fire once now for the new height
	// since there are still txs left
	committedTxs, txs := txs[:50], txs[50:]
	updateMempool(ctx, t, mp, 1, committedTxs, abciResponses(len(committedTxs), abci.CodeTypeOK), nil, nil)
	ensureFire(t, mp.TxsAvailable(), timeoutMS)
	ensureNoFire(t, mp.TxsAvailable(), timeoutMS)

	// send a bunch more txs. we already fired for this height so it shouldnt fire again
	moreTxs := checkTxs(t, mp, 50, UnknownPeerID)
	ensureNoFire(t, mp.TxsAvailable(), timeoutMS)

	// now call update with all the txs. it should not fire as there are no txs left
	committedTxs = append(txs, moreTxs...) //nolint: gocritic
	updateMempool(ctx, t, mp, 2, committedTxs, abciResponses(len(committedTxs), abci.CodeTypeOK), nil, nil)
	ensureNoFire(t, mp.TxsAvailable(), timeoutMS)

	// send a bunch more txs, it should only fire once
	checkTxs(t, mp, 100, UnknownPeerID)
	ensureFire(t, mp.TxsAvailable(), timeoutMS)
	ensureNoFire(t, mp.TxsAvailable(), timeoutMS)
}

func TestSerialReap(t *testing.T) {
	app := counter.NewApplication(true)
	app.SetOption(abci.RequestSetOption{Key: "serial", Value: "on"})
	cc := proxy.NewLocalClientCreator(app)

	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()

	appConnCon, _ := cc.NewABCIClient()
	appConnCon.SetLogger(log.TestingLogger().With("module", "abci-client", "connection", "consensus"))
	err := appConnCon.Start()
	require.Nil(t, err)

	cacheMap := make(map[string]struct{})
	deliverTxsRange := func(start, end int) {
		// Deliver some txs.
		for i := start; i < end; i++ {

			// This will succeed
			txBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(txBytes, uint64(i))
			err := mp.CheckTx(ctx, txBytes, nil, TxInfo{})
			_, cached := cacheMap[string(txBytes)]
			if cached {
				require.NotNil(t, err, "expected error for cached tx")
			} else {
				require.Nil(t, err, "expected no err for uncached tx")
			}
			cacheMap[string(txBytes)] = struct{}{}

			// Duplicates are cached and should return error
			err = mp.CheckTx(ctx, txBytes, nil, TxInfo{})
			require.NotNil(t, err, "Expected error after CheckTx on duplicated tx")
		}
	}

	reapCheck := func(t *testing.T, exp int) {
		t.Helper()

		reaper, err := mp.Reap(ctx)
		require.NoError(t, err)

		txs, err := reaper.Txs(ctx)
		require.NoError(t, err)
		require.Equal(t, len(txs), exp, fmt.Sprintf("Expected to reap %v txs but got %v", exp, len(txs)))
	}

	updateRange := func(t *testing.T, start, end int) {
		t.Helper()

		txs := make([]types.Tx, 0)
		for i := start; i < end; i++ {
			txBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(txBytes, uint64(i))
			txs = append(txs, txBytes)
		}
		updateMempool(ctx, t, mp, 0, txs, abciResponses(len(txs), abci.CodeTypeOK), nil, nil)
	}

	commitRange := func(t *testing.T, start, end int) {
		t.Helper()

		// Deliver some txs.
		for i := start; i < end; i++ {
			txBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(txBytes, uint64(i))
			res, err := appConnCon.DeliverTxSync(abci.RequestDeliverTx{Tx: txBytes})
			if err != nil {
				t.Errorf("client error committing tx: %v", err)
			}
			if res.IsErr() {
				t.Errorf("error committing tx. Code:%v result:%X log:%v",
					res.Code, res.Data, res.Log)
			}
		}
		res, err := appConnCon.CommitSync()
		if err != nil {
			t.Errorf("client error committing: %v", err)
		}
		if len(res.Data) != 8 {
			t.Errorf("error committing. Hash:%X", res.Data)
		}
	}

	// ----------------------------------------

	// Deliver some txs.
	deliverTxsRange(0, 100)

	// Reap the txs.
	reapCheck(t, 100)

	// Reap again.  We should get the same amount
	reapCheck(t, 100)

	// Deliver 0 to 999, we should reap 900 new txs
	// because 100 were already counted.
	deliverTxsRange(0, 1000)

	// Reap the txs.
	reapCheck(t, 1000)

	// Reap again.  We should get the same amount
	reapCheck(t, 1000)

	// Commit from the conensus AppConn
	commitRange(t, 0, 500)
	updateRange(t, 0, 500)

	// We should have 500 left.
	reapCheck(t, 500)

	// Deliver 100 invalid txs and 100 valid txs
	deliverTxsRange(900, 1100)

	// We should have 600 now.
	reapCheck(t, 600)
}

func TestMempoolCloseWAL(t *testing.T) {
	// 1. Create the temporary directory for mempool and WAL testing.
	rootDir, err := ioutil.TempDir("", "mempool-test")
	require.Nil(t, err, "expecting successful tmpdir creation")

	// 2. Ensure that it doesn't contain any elements -- Sanity check
	m1, err := filepath.Glob(filepath.Join(rootDir, "*"))
	require.Nil(t, err, "successful globbing expected")
	require.Equal(t, 0, len(m1), "no matches yet")

	// 3. Create the mempool
	wcfg := cfg.DefaultConfig()
	wcfg.Mempool.RootDir = rootDir
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	clist, mp, cleanup := newMempoolWithAppAndConfig(cc, wcfg)
	defer cleanup()

	ctx := context.TODO()

	clist.height = 10
	err = clist.InitWAL()
	require.NoError(t, err)

	// 4. Ensure that the directory contains the WAL file
	m2, err := filepath.Glob(filepath.Join(rootDir, "*"))
	require.Nil(t, err, "successful globbing expected")
	require.Equal(t, 1, len(m2), "expecting the wal match in")

	// 5. Write some contents to the WAL
	err = mp.CheckTx(ctx, types.Tx([]byte("foo")), nil, TxInfo{})
	require.NoError(t, err)
	walFilepath := clist.wal.Path
	sum1 := checksumFile(walFilepath, t)

	// 6. Sanity check to ensure that the written TX matches the expectation.
	require.Equal(t, sum1, checksumIt([]byte("foo\n")), "foo with a newline should be written")

	// 7. Invoke CloseWAL() and ensure it discards the
	// WAL thus any other write won't go through.
	clist.CloseWAL()
	err = mp.CheckTx(ctx, types.Tx([]byte("bar")), nil, TxInfo{})
	require.NoError(t, err)
	sum2 := checksumFile(walFilepath, t)
	require.Equal(t, sum1, sum2, "expected no change to the WAL after invoking CloseWAL() since it was discarded")

	// 8. Sanity check to ensure that the WAL file still exists
	m3, err := filepath.Glob(filepath.Join(rootDir, "*"))
	require.Nil(t, err, "successful globbing expected")
	require.Equal(t, 1, len(m3), "expecting the wal match in")
}

func TestMempool_CheckTxChecksTxSize(t *testing.T) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()
	maxTxSize := mp.cfg.MaxTxBytes

	testCases := []struct {
		len int
		err bool
	}{
		// check small txs. no error
		0: {10, false},
		1: {1000, false},
		2: {1000000, false},

		// check around maxTxSize
		3: {maxTxSize - 1, false},
		4: {maxTxSize, false},
		5: {maxTxSize + 1, true},
	}

	for i, testCase := range testCases {
		caseString := fmt.Sprintf("case %d, len %d", i, testCase.len)

		tx := tmrand.Bytes(testCase.len)

		err := mp.CheckTx(ctx, tx, nil, TxInfo{})
		bv := gogotypes.BytesValue{Value: tx}
		bz, err2 := bv.Marshal()
		require.NoError(t, err2)
		require.Equal(t, len(bz), proto.Size(&bv), caseString)

		if !testCase.err {
			require.NoError(t, err, caseString)
		} else {
			require.Equal(t, err, ErrTxTooLarge{maxTxSize, testCase.len}, caseString)
		}
	}
}

func TestMempoolTxsBytes(t *testing.T) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	config := cfg.ResetTestRoot("mempool_test")
	config.Mempool.MaxTxsBytes = 10
	_, mp, cleanup := newMempoolWithAppAndConfig(cc, config)
	defer cleanup()

	ctx := context.TODO()

	// 1. zero by default
	assert.Zero(t, mp.PoolMeta().TotalBytes)

	// 2. len(tx) after CheckTx
	err := mp.CheckTx(ctx, []byte{0x01}, nil, TxInfo{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, mp.PoolMeta().TotalBytes)

	// 3. zero again after tx is removed by Update
	updateMempool(ctx, t, mp, 1, []types.Tx{[]byte{0x01}}, abciResponses(1, abci.CodeTypeOK), nil, nil)
	require.NoError(t, err)
	assert.Zero(t, mp.PoolMeta().TotalBytes)

	// 4. zero after Flush
	err = mp.CheckTx(ctx, []byte{0x02, 0x03}, nil, TxInfo{})
	require.NoError(t, err)
	assert.EqualValues(t, 2, mp.PoolMeta().TotalBytes)

	require.NoError(t, mp.Flush(ctx))
	assert.Zero(t, mp.PoolMeta().TotalBytes)

	// 5. ErrMempoolIsFull is returned when/if MaxTxsBytes limit is reached.
	err = mp.CheckTx(ctx, []byte{0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04}, nil, TxInfo{})
	require.NoError(t, err)
	err = mp.CheckTx(ctx, []byte{0x05}, nil, TxInfo{})
	if assert.Error(t, err) {
		assert.IsType(t, ErrMempoolIsFull{}, err)
	}

	// 6. zero after tx is rechecked and removed due to not being valid anymore
	app2 := counter.NewApplication(true)
	cc = proxy.NewLocalClientCreator(app2)
	_, mp, cleanup = newMempoolWithApp(cc)
	defer cleanup()

	txBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(txBytes, uint64(0))

	err = mp.CheckTx(ctx, txBytes, nil, TxInfo{})
	require.NoError(t, err)
	assert.EqualValues(t, 8, mp.PoolMeta().TotalBytes)

	appConnCon, _ := cc.NewABCIClient()
	appConnCon.SetLogger(log.TestingLogger().With("module", "abci-client", "connection", "consensus"))
	err = appConnCon.Start()
	require.Nil(t, err)
	t.Cleanup(func() {
		if err := appConnCon.Stop(); err != nil {
			t.Error(err)
		}
	})
	res, err := appConnCon.DeliverTxSync(abci.RequestDeliverTx{Tx: txBytes})
	require.NoError(t, err)
	require.EqualValues(t, 0, res.Code)
	res2, err := appConnCon.CommitSync()
	require.NoError(t, err)
	require.NotEmpty(t, res2.Data)

	// Pretend like we committed nothing so txBytes gets rechecked and removed.
	updateMempool(ctx, t, mp, 1, []types.Tx{}, abciResponses(0, abci.CodeTypeOK), nil, nil)
	require.NoError(t, err)
	assert.Zero(t, mp.PoolMeta().TotalBytes)

	// 7. Test RemoveTxByKey function
	err = mp.CheckTx(ctx, []byte{0x06}, nil, TxInfo{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, mp.PoolMeta().TotalBytes)

	// deleting 0x07 tx should not delete anything
	err = mp.Remove(ctx, RemByTxKeys(TxKey([]byte{0x07})))
	require.NoError(t, err)
	assert.EqualValues(t, 1, mp.PoolMeta().TotalBytes)

	// deleting 0x06 wil delete the existing tx
	err = mp.Remove(ctx, RemByTxKeys(TxKey([]byte{0x06})))
	require.NoError(t, err)
	assert.Zero(t, mp.PoolMeta().TotalBytes)
}

// This will non-deterministically catch some concurrency failures like
// https://github.com/tendermint/tendermint/issues/3509
// TODO: all of the tests should probably also run using the remote proxy app
// since otherwise we're not actually testing the concurrency of the mempool here!
func TestMempoolRemoteAppConcurrency(t *testing.T) {
	sockPath := fmt.Sprintf("unix:///tmp/echo_%v.sock", tmrand.Str(6))
	app := kvstore.NewApplication()
	cc, server := newRemoteApp(t, sockPath, app)
	t.Cleanup(func() {
		if err := server.Stop(); err != nil {
			t.Error(err)
		}
	})
	config := cfg.ResetTestRoot("mempool_test")
	_, mp, cleanup := newMempoolWithAppAndConfig(cc, config)
	defer cleanup()

	ctx := context.TODO()

	// generate small number of txs
	nTxs := 10
	txLen := 200
	txs := make([]types.Tx, nTxs)
	for i := 0; i < nTxs; i++ {
		txs[i] = tmrand.Bytes(txLen)
	}

	// simulate a group of peers sending them over and over
	N := config.Mempool.Size
	maxPeers := 5
	for i := 0; i < N; i++ {
		peerID := mrand.Intn(maxPeers)
		txNum := mrand.Intn(nTxs)
		tx := txs[txNum]

		// this will err with ErrTxInCache many times ...
		mp.CheckTx(ctx, tx, nil, TxInfo{SenderID: uint16(peerID)}) //nolint: errcheck // will error
	}
	err := mp.appConn.FlushSync()
	require.NoError(t, err)
}

// caller must close server
func newRemoteApp(
	t *testing.T,
	addr string,
	app abci.Application,
) (
	clientCreator proxy.ClientCreator,
	server service.Service,
) {
	clientCreator = proxy.NewRemoteClientCreator(addr, "socket", true)

	// Start server
	server = abciserver.NewSocketServer(addr, app)
	server.SetLogger(log.TestingLogger().With("module", "abci-server"))
	if err := server.Start(); err != nil {
		t.Fatalf("Error starting socket server: %v", err.Error())
	}
	return clientCreator, server
}
func checksumIt(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func checksumFile(p string, t *testing.T) string {
	data, err := ioutil.ReadFile(p)
	require.Nil(t, err, "expecting successful read of %q", p)
	return checksumIt(data)
}

func abciResponses(n int, code uint32) []*abci.ResponseDeliverTx {
	responses := make([]*abci.ResponseDeliverTx, 0, n)
	for i := 0; i < n; i++ {
		responses = append(responses, &abci.ResponseDeliverTx{Code: code})
	}
	return responses
}
