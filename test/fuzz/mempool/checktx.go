package checktx

import (
	"context"

	"github.com/tendermint/tendermint/abci/example/kvstore"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	mempl "github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/proxy"
)

var mempool *mempl.ABCI

func init() {
	app := kvstore.NewApplication()
	cc := proxy.NewLocalClientCreator(app)
	appConnMem, _ := cc.NewABCIClient()
	err := appConnMem.Start()
	if err != nil {
		panic(err)
	}

	cfg := config.DefaultMempoolConfig()
	cfg.Broadcast = false

	mempool = mempl.NewABCI(
		log.TestingLogger(),
		cfg,
		appConnMem,
		mempl.NewPoolCList(cfg, 0),
	)
}

func Fuzz(data []byte) int {
	ctx := context.TODO()
	err := mempool.CheckTx(ctx, data, nil, mempl.TxInfo{})
	if err != nil {
		return 0
	}

	return 1
}
