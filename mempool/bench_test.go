package mempool

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/abci/example/kvstore"
	"github.com/tendermint/tendermint/proxy"
)

func BenchmarkReap(b *testing.B) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()

	size := 10000
	for i := 0; i < size; i++ {
		tx := make([]byte, 8)
		binary.BigEndian.PutUint64(tx, uint64(i))
		if err := mp.CheckTx(ctx, tx, nil, TxInfo{}); err != nil {
			b.Error(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reaper, err := mp.Reap(ctx, ReapBytes(100000000), ReapGas(10000000))
		require.NoError(b, err)

		reaper.Txs(ctx)
	}
}

func BenchmarkCheckTx(b *testing.B) {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	_, mp, cleanup := newMempoolWithApp(cc)
	defer cleanup()

	ctx := context.TODO()

	for i := 0; i < b.N; i++ {
		tx := make([]byte, 8)
		binary.BigEndian.PutUint64(tx, uint64(i))
		if err := mp.CheckTx(ctx, tx, nil, TxInfo{}); err != nil {
			b.Error(err)
		}
	}
}

func BenchmarkCacheInsertTime(b *testing.B) {
	cache := newMapTxCache(b.N)
	txs := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		txs[i] = make([]byte, 8)
		binary.BigEndian.PutUint64(txs[i], uint64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Push(txs[i])
	}
}

// This benchmark is probably skewed, since we actually will be removing
// txs in parallel, which may cause some overhead due to mutex locking.
func BenchmarkCacheRemoveTime(b *testing.B) {
	cache := newMapTxCache(b.N)
	txs := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		txs[i] = make([]byte, 8)
		binary.BigEndian.PutUint64(txs[i], uint64(i))
		cache.Push(txs[i])
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Remove(txs[i])
	}
}
