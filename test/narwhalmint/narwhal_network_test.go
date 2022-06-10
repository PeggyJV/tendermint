package narwhalmint_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/test/narwhalmint"
)

const (
	kilobyte = 1 << 10
)

/*
To run teh tests simply run the normal go test .
You may parameterize the test setup by providing it an env var
to overwrite the default values. The env vars are listed with default
values in parens as follows:
	NARWHAL_WIPE (false)
	NARWHAL_PRIMARIES (4)
	NARWHAL_WORKERS (1)

Be mindful of overwriting the primaries, they should be able to reach consensus.
4 or 7 are good numbers for local testing. Scale the workers as you see fit.
*/
func TestNarwhalNetwork(t *testing.T) {
	shouldCleanupRaw := os.Getenv("NARWHAL_WIPE")
	shouldCleanup := shouldCleanupRaw == "true" || shouldCleanupRaw == "1" || shouldCleanupRaw == "yes"
	if shouldCleanup {
		t.Log("test will cleanup directory artifacts upon completion")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := narwhalmint.LauncherNarwhal{
		BlockSizeLimitBytes: 50 * kilobyte, // 10kbs
		Host:                "127.0.0.1",
		Primaries:           envOrIntDefault("NARWHAL_PRIMARIES", 4),
		Workers:             envOrIntDefault("NARWHAL_WORKERS", 1),
		Out:                 os.Stdout,
	}
	defer l.Close()
	t.Logf("configured with %d primaries with %d workers and block size %d bytes", l.Primaries, l.Workers, l.BlockSizeLimitBytes)

	require.NoError(t, l.SetupFS(ctx, time.Now()))
	t.Log("tests dir located at: ", l.Dir())

	defer func() {
		if shouldCleanup {
			assert.NoError(t, os.RemoveAll(l.Dir()))
		}
	}()

	require.NoError(t, l.Start(ctx))

	waitDir := 3 * time.Second
	t.Logf("waiting %s for narwhal cluster to be ready", waitDir)
	time.Sleep(waitDir)

	t.Log("initializing nodes")

	done := make(chan struct{})
	go func() {
		defer close(done)

		for runNodeErr := range l.NodeRuntimeErrs() {
			t.Log("error running node: ", runNodeErr)
		}
	}()

	maxTXs := 500
	t.Logf("submitting %d txs", maxTXs)
	for i := 0; i < maxTXs; i++ {
		c := l.NextTransactionClient()
		meta := c.Meta()
		txPayload := []byte("payload-" + strconv.Itoa(i))
		err := c.SubmitTransaction(ctx, txPayload)
		if err != nil {
			t.Logf("faled to submit TX for %s: %s", meta, err)
			continue
		}
	}
	t.Logf("submitted %d txs", maxTXs)

	t.Logf("waiting %s for narwhal nodes to propogate txs and create collections", waitDir)
	time.Sleep(waitDir)
	t.Log("setup complete\n")

	proposeBlockRun(t, ctx, &l, 0)
	proposeBlockRun(t, ctx, &l, 1)

	t.Log("terminating narwhal_nodes...")
	cancel()
	<-done
}

func proposeBlockRun(t *testing.T, ctx context.Context, l *narwhalmint.LauncherNarwhal, blockNum int) {
	t.Helper()

	proposer := l.NextPrimaryClient()
	t.Logf("getting next block %d collections from %s...", blockNum, proposer.Meta())

	nextBlockCollections, err := proposer.NextBlockCollections(ctx)
	require.NoError(t, err)
	if len(nextBlockCollections) == 0 {
		t.Logf("block %d has no collections associated with it", blockNum)
		return
	}

	startingCollection := nextBlockCollections[0]
	t.Logf("DAG walk starting coll(%s)", base64Str(startingCollection.Digest))
	if colls := nextBlockCollections; len(colls) > 0 {
		t.Logf("got %d colls: starting coll(%s) and final coll(%s)", len(colls), base64Str(colls[0].Digest), base64Str(colls[len(colls)-1].Digest))
	}

	validator := l.NextPrimaryClient()
	t.Logf("getting collections from starting coll(%s) from %s...", base64Str(startingCollection.Digest), validator.Meta())

	colls, err := validator.CollectionsFrom(ctx, startingCollection)
	require.NoError(t, err)
	if len(colls) > 0 {
		t.Logf("got %d colls: starting coll(%s) and final coll(%s)", len(colls), base64Str(colls[0].Digest), base64Str(colls[len(colls)-1].Digest))
	}

	t.Logf("getting transactions from %s", validator.Meta())
	txs, err := validator.CollectionTXs(ctx, nextBlockCollections...)
	require.NoError(t, err)

	if len(txs) > 0 {
		sort.Slice(txs, func(i, j int) bool {
			prefix := []byte("payload-")
			ii, jj := bytes.TrimPrefix(txs[i], prefix), bytes.TrimPrefix(txs[j], prefix)
			iInt, _ := strconv.Atoi(string(ii))
			jInt, _ := strconv.Atoi(string(jj))
			return iInt < jInt
		})
		t.Logf("received %d txs.... start tx %s and final tx %s", len(txs), string(txs[0]), string(txs[len(txs)-1]))
	}

	t.Log("---handwaving--- consensus reached")

	t.Log("removing collections from primaries")
	// we delete collections from all primaries
	for i := 0; i < l.Primaries; i++ {
		nextValidator := l.NextPrimaryClient()
		err := nextValidator.RemoveCollections(ctx, nextBlockCollections...)
		require.NoError(t, err)
	}
	t.Log("collections removed from all primaries\n")
}

func base64Str(src []byte) string {
	return base64.StdEncoding.EncodeToString(src)
}

func envOrIntDefault(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}

	i, _ := strconv.Atoi(strings.TrimSpace(v))
	return i
}
