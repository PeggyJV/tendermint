package docker_run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	tmhttp "github.com/tendermint/tendermint/rpc/client/http"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

func Test_Txs(t *testing.T) {
	ctx := context.Background()

	batchSize, headerSize := paramsFile(t, "validators/parameters.json")
	c := readCompose(t, "docker-compose.yml")
	clients := c.tmClients(t)

	waitFn := cleanStart(ctx, t, "compose.logs", clientIDs(clients...), 15*time.Second)
	defer waitFn()

	printSyncInfo(ctx, t, clients)

	done := runTxs(ctx, t, runTxsIn{
		batchSize:    batchSize,
		clients:      clients,
		headerSize:   headerSize,
		numTxs:       30000,
		txsPerClient: 200,
	})
	for {
		select {
		case <-ctx.Done():
			t.Log("exiting on context cancellation")
			return
		case <-done:
			t.Log("txs have completed")
			printSyncInfo(ctx, t, clients)
			return
		case <-time.After(2 * time.Second):
			printSyncInfo(ctx, t, clients)
		}
	}
}

func TestResetDirs(t *testing.T) {
	c := readCompose(t, "docker-compose.yml")
	for _, node := range c.tmNodes() {
		resetDirs(t, node.tmNodeID())
	}
}

func cleanStart(ctx context.Context, t *testing.T, logFile string, nodeIDs []string, waitDur time.Duration) func() {
	t.Helper()

	t.Log("cleaning up prev artifacts and containers...")
	mustComposeDown(ctx, t) // make sure we start off clean, if not running... a nothing burger
	resetDirs(t, nodeIDs...)

	out := composeUp(ctx, t, logFile, waitDur)
	return func() {
		mustComposeDown(ctx, t)
		<-out
	}
}

func resetDirs(t *testing.T, nodeIDs ...string) {
	t.Helper()

	dirFmts := []string{
		"build/node%s/tendermint.log",
		"build/node%s/data/blockstore.db",
		"build/node%s/data/cs.wal",
		"build/node%s/data/evidence.db",
		"build/node%s/data/state.db",
		"build/node%s/data/tx_index.db",
		"build/node%s/config/addrbook.json",
		"validators/validator-%s/db-primary",
		"validators/validator-%s/db-worker-0",
	}

	for _, nodeID := range nodeIDs {
		for _, dirFmt := range dirFmts {
			dir := fmt.Sprintf(dirFmt, nodeID)
			t.Log("removing ", dir)
			err := os.RemoveAll(dir)
			require.NoError(t, err)
		}

		privStateFile := fmt.Sprintf("build/node%s/data/priv_validator_state.json", nodeID)
		t.Log("resetting ", privStateFile)
		resetPrivStateFile(t, privStateFile)
	}
}

func resetPrivStateFile(t *testing.T, filename string) {
	t.Helper()

	var priv struct {
		Signature string `json:"signature"`
		SignBytes string `json:"signbytes"`
	}

	bb, err := json.MarshalIndent(priv, "", "  ")
	require.NoError(t, err)

	err = os.WriteFile(filename, bb, os.ModePerm)
	require.NoError(t, err)
}

type runTxsIn struct {
	batchSize    int
	clients      []*client
	headerSize   int
	numTxs       int
	txsPerClient int
	start        int
}

func runTxs(ctx context.Context, t *testing.T, in runTxsIn) <-chan struct{} {
	t.Helper()

	out := make(chan struct{})

	go func(start, maxTxs int, cls []*client) {
		defer close(out)

		startTime := time.Now()

		errStream := make(chan error)
		go func() {
			defer close(errStream)

			wg := new(sync.WaitGroup)
			sem := make(chan struct{}, len(cls)*in.txsPerClient)
			t.Log("commencing tx submission", maxTxs, " txs")
			for i := start; i < start+maxTxs; i++ {
				if i%(maxTxs/50) == 0 {
					t.Log("sending ", i, "th tx")
				}
				sem <- struct{}{}
				wg.Add(1)
				go func(txID int, client *client) {
					defer wg.Done()
					defer func() { <-sem }()

					err := client.SubmitTxAsync(ctx, t, []byte(fmt.Sprintf("tx%d", txID)))
					if err != nil {
						errStream <- err
					}
				}(i, cls[i%4])
			}
			wg.Wait()
		}()

		mErrs := make(map[string]int)
		for err := range errStream {
			errMsg := err.Error()
			if strings.HasSuffix(errMsg, "connection reset by peer") {
				parts := strings.Split(errMsg, ": ")
				parts = append(parts[:2], parts[3:]...)
				errMsg = strings.Join(parts, ": ")
			}
			mErrs[errMsg]++
		}

		if len(mErrs) > 0 {
			t.Log("Errors encountered submitting txs: ")
			for errMsg, count := range mErrs {
				t.Logf("\tcount=%d\terr=%s", count, errMsg)
			}
		}

		t.Log("client tx submissions stats:")
		total := 0
		for _, cl := range cls {
			total += cl.txsSubmitted
			t.Log("\t", cl.name, " submitted ", cl.txsSubmitted, " txs")
		}
		t.Logf("submitted %d/%d(%0.3f%%) txs in %s with %d reqs/client batch size %d and header size %d",
			total, maxTxs, (float64(total)/float64(maxTxs))*100, time.Since(startTime), in.txsPerClient, in.batchSize, in.headerSize,
		)
	}(in.start, in.numTxs, in.clients)

	return out
}

func printSyncInfo(ctx context.Context, t *testing.T, clients []*client) {
	t.Helper()

	m := make(map[string]*coretypes.ResultStatus, len(clients))
	for _, cl := range clients {
		status, err := cl.Status(ctx)
		if err != nil {
			t.Logf("failed to get %s node status: %s", cl.name, err)
			continue
		}
		m[cl.name] = status
	}

	var parts []string
	for name, status := range m {
		parts = append(parts,
			fmt.Sprintf("%s: { lbh: %d, lbt: %s, ebh: %d, ebt: %s}",
				name,
				status.SyncInfo.LatestBlockHeight, status.SyncInfo.LatestBlockTime.Local().Format(time.Kitchen),
				status.SyncInfo.EarliestBlockHeight, status.SyncInfo.EarliestBlockTime.Local().Format(time.Kitchen),
			),
		)
	}
	if len(parts) == 0 {
		return
	}
	nodeIDFn := func(raw string) string { return strings.Split(raw, ":")[0] }
	sort.Slice(parts, func(i, j int) bool {
		return nodeIDFn(parts[i]) < nodeIDFn(parts[j])
	})
	t.Logf(strings.Join(parts, "\t"))
}

func composeUp(ctx context.Context, t *testing.T, logFile string, waitDir time.Duration) <-chan struct{} {
	t.Helper()

	logs, err := os.Create(logFile)
	require.NoError(t, err)

	out := make(chan struct{})
	go func(f *os.File) {
		defer close(out)
		defer f.Close()
		// ignore the error here
		cmdCompose(ctx, t, f, "up")
	}(logs)
	t.Logf("waiting %s for startup to complete...", waitDir.String())
	time.Sleep(waitDir)

	return out
}

func mustComposeDown(ctx context.Context, t *testing.T) {
	t.Helper()
	err := cmdCompose(ctx, t, io.Discard, "down")
	require.NoError(t, err)
}

func cmdCompose(ctx context.Context, t *testing.T, out io.Writer, cmdName string, additionalArgs ...string) error {
	t.Helper()

	args := append([]string{"compose", cmdName}, additionalArgs...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}

	t.Logf("docker compose %s commencing...", cmdName)

	err = cmd.Wait()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	t.Logf("docker compose %s completed", cmdName)

	return nil
}

type client struct {
	mu           sync.Mutex
	name         string
	id           string
	http         *tmhttp.HTTP
	txsSubmitted int
}

func (c *client) Status(ctx context.Context, ) (*coretypes.ResultStatus, error) {
	return c.http.Status(ctx)
}

func (c *client) SubmitTxAsync(ctx context.Context, t *testing.T, tx types.Tx) error {
	t.Helper()

	_, err := c.http.BroadcastTxAsync(ctx, tx)
	if err == nil {
		c.mu.Lock()
		c.txsSubmitted++
		c.mu.Unlock()
	}
	return err
}

func clientIDs(clients ...*client) []string {
	out := make([]string, 0, len(clients))
	for _, c := range clients {
		out = append(out, c.id)
	}
	return out
}

type compose struct {
	Services map[string]service `yaml:"services"`
}

func (c compose) tmNodes() map[string]service {
	out := make(map[string]service)
	for k, serv := range c.Services {
		if serv.tmNodeID() == "" {
			continue
		}
		out[k] = serv
	}
	return out
}

func (c compose) tmClients(t *testing.T) []*client {
	t.Helper()

	var out []*client
	for k, serv := range c.tmNodes() {
		cl := serv.tmClient(t)
		cl.name = k
		out = append(out, cl)
	}
	return out
}

type service struct {
	Environment []string `yaml:"environment"`
	Expose      []string `yaml:"expose"`
	Ports       []string `yaml:"ports"`
	Networks    map[string]struct {
		IPV4Addr string `yaml:"ipv4_address"`
	} `yaml:"networks"`
}

func (s service) tmNodeID() string {
	for _, ss := range s.Environment {
		prefix := "ID="
		if !strings.HasPrefix(ss, prefix) {
			continue
		}
		return strings.TrimPrefix(ss, prefix)
	}
	return ""
}

func (s service) tmClient(t *testing.T) *client {
	t.Helper()

	for _, ss := range s.Ports {
		lhsPorts := strings.Split(ss, ":")[0]
		ports := strings.Split(lhsPorts, "-")
		require.Len(t, ports, 2)

		// TODO(berg): this is the source of the reset conn errors....
		//				and for some odd reason, the flakiness of it, is infuriating b/c
		//				when I inject the http client, setting max idle conns to some nonzero
		//				value... it blows up with an EOF. Makes no sense to me atm
		remote := "tcp://127.0.0.1:" + ports[1]
		tmHTTP, err := tmhttp.NewWithTimeout(remote, "/websockets", 30)
		require.NoError(t, err)

		return &client{
			id:   s.tmNodeID(),
			http: tmHTTP,
		}
	}

	require.FailNow(t, "failed to create valid client", s)

	return nil
}

// ParseConfig retrieves the default environment configuration,
// sets up the Tendermint root and ensures that the root exists
func readCompose(t *testing.T, filename string) compose {
	t.Helper()

	f, err := os.Open(filename)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()

	var c compose
	err = yaml.
		NewDecoder(f).
		Decode(&c)
	require.NoError(t, err)

	return c
}

func paramsFile(t *testing.T, filename string) (batchSize, headerSize int) {
	t.Helper()

	bb, err := os.ReadFile(filename)
	require.NoError(t, err)

	var m struct {
		BatchSize  int `json:"batch_size"`
		HeaderSize int `json:"header_size"`
	}
	err = json.Unmarshal(bb, &m)
	require.NoError(t, err)

	return m.BatchSize, m.HeaderSize
}
