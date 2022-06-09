package narwhalmint_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/test/narwhalmint"
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
		Host:              "127.0.0.1",
		ParameterContents: parameterFileContents,
		Primaries:         envOrIntDefault("NARWHAL_PRIMARIES", 4),
		Workers:           envOrIntDefault("NARWHAL_WORKERS", 1),
		Out:               os.Stdout,
	}
	t.Logf("configured with %d primaries with %d workers", l.Primaries, l.Workers)

	require.NoError(t, l.SetupFS(ctx, time.Now()))
	t.Log("tests dir located at: ", l.TestRootDir())

	defer func() {
		if shouldCleanup {
			assert.NoError(t, os.RemoveAll(l.TestRootDir()))
		}
	}()

	t.Log("initializing nodes")
	for runNodeErr := range l.RunNarwhalNodes(ctx) {
		t.Log("error running node: ", runNodeErr)
	}
}

func envOrIntDefault(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}

	i, _ := strconv.Atoi(strings.TrimSpace(v))
	return i
}

// contents pulled from mystenlabs/narwhal demo
var parameterFileContents = `
{
    "batch_size": 5,
    "block_synchronizer": {
        "certificates_synchronize_timeout": "2_000ms",
        "handler_certificate_deliver_timeout": "2_000ms",
        "payload_availability_timeout": "2_000ms",
        "payload_synchronize_timeout": "2_000ms"
    },
    "consensus_api_grpc": {
        "get_collections_timeout": "5_000ms",
        "remove_collections_timeout": "5_000ms",
        "socket_addr": "/ip4/0.0.0.0/tcp/0/http"
    },
    "gc_depth": 50,
    "header_size": 1000,
    "max_batch_delay": "200ms",
    "max_concurrent_requests": 500000,
    "max_header_delay": "2000ms",
    "sync_retry_delay": "10_000ms",
    "sync_retry_nodes": 3
}`
