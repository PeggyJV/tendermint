//go:build narwhal

package narwhalmint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmmath "github.com/tendermint/tendermint/libs/math"
	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalmint"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

const truncTo = time.Millisecond

func Benchmark_TM_Narwhal(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer func() {
		cancel()
		wait(b, 5, "waiting for test and TIME_WAIT to cool down")
	}()

	start := time.Now().Truncate(truncTo)
	_, ltm := startDefaultNarwhalTMNodes(ctx, b, narwhalTMOpts{Start: start})

	clients := ltm.Clients()

	runner := newTMClientRunner(clients, 100_000, 100)
	defer func() {
		runner.printRunStats(b)
		writeTestStats(b, narwhalmint.TestDir(start), start, runner.runtimeStats())
	}()

	b.Log(nowTS(), "submitting client Txs: max_txs=", runner.maxTxs, " max_concurrent=", runner.maxConcurrent, " txs/client: ", runner.totalTxsPerClient)
	b.ResetTimer()
	b.StartTimer()
	defer b.StopTimer()

	streamOn(ctx, runner.watchClientTxSubmissions(ctx, b, 2*time.Second))
}

func Test_TM_Narwhal_resume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	rootDir := filepath.Join(os.ExpandEnv("$PWD/test_results"), "Aug-22-14:12:25")
	if !dirExists(rootDir) {
		t.Skipf("skipping test root dir does not exist: root_dir=%s", rootDir)
	}

	start := time.Now()

	_, ltm := startDefaultNarwhalTMNodes(ctx, t, narwhalTMOpts{
		Start:     start,
		StartFrom: rootDir,
	})

	clients := ltm.Clients()

	runner := newTMClientRunner(clients, 400_000, 100)
	defer func() {
		runner.printRunStats(t)
		writeTestStats(t, rootDir, start, runner.runtimeStats())
	}()

	t.Log(nowTS(), "submitting client Txs: max_txs=", runner.maxTxs, " max_concurrent=", runner.maxConcurrent, " txs/client: ", runner.totalTxsPerClient)
	streamOn(ctx, runner.watchClientTxSubmissions(ctx, t, 2*time.Second))
}

func Test_TM_Narwhal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	start := time.Now()

	_, ltm := startDefaultNarwhalTMNodes(ctx, t, narwhalTMOpts{
		BatchSize:    5000,
		HeaderSize:   300,
		ProxyAppType: "kvstore",
		Start:        start,
	})

	clients := ltm.Clients()

	runner := newTMClientRunner(clients, 300_000, 10)
	defer func() {
		runner.printRunStats(t)
		writeTestStats(t, narwhalmint.TestDir(start), start, runner.runtimeStats())
	}()

	t.Log(nowTS(), "submitting client Txs: max_txs=", runner.maxTxs, " max_concurrent=", runner.maxConcurrent, " txs/client: ", runner.totalTxsPerClient)
	streamOn(ctx, runner.watchClientTxSubmissions(ctx, t, 2*time.Second))

	runner.reportNodeStatuses(ctx, t, clients)
}

type narwhalTMOpts struct {
	BatchSize      int
	HeaderSize     int
	LogLvl         string
	NarwhalHost    string
	Primaries      int
	Workers        int
	ProxyAppType   string
	Out            io.Writer
	RunTMInProcess bool
	Start          time.Time
	StartFrom      string
}

func streamOn[T any](ctx context.Context, stream <-chan T) bool {
	select {
	case <-ctx.Done():
		return false
	case <-stream:
		return true
	}
}

func startDefaultNarwhalTMNodes(ctx context.Context, tb testing.TB, opts narwhalTMOpts) (*narwhalmint.LauncherNarwhal, *narwhalmint.LauncherTendermint) {
	tb.Helper()

	if opts.Start.IsZero() {
		opts.Start = time.Now()
	}

	lnarwhal := startDefaultNarwhalNodes(ctx, tb, opts)
	ltm := startDefaultTMNodes(ctx, tb, lnarwhal.TMOpts("", ""), opts)

	return lnarwhal, ltm
}

func startDefaultNarwhalNodes(ctx context.Context, tb testing.TB, opts narwhalTMOpts) *narwhalmint.LauncherNarwhal {
	tb.Helper()

	if opts.Start.IsZero() {
		opts.Start = time.Now()
	}
	lnarwhal := narwhalmint.LauncherNarwhal{
		BatchSize:  getEnvOrDefault("BATCH_SIZE", 1<<14),
		HeaderSize: getEnvOrDefault("HEADER_SIZE", 1<<9),
		Host:       "127.0.0.1",
		Primaries:  4,
		Workers:    1,
		Out:        opts.Out,
	}
	if opts.NarwhalHost != "" {
		lnarwhal.Host = opts.NarwhalHost
	}
	if opts.BatchSize > 0 {
		lnarwhal.BatchSize = opts.BatchSize
	}
	if opts.HeaderSize > 0 {
		lnarwhal.HeaderSize = opts.HeaderSize
	}
	if opts.Primaries > 0 {
		lnarwhal.Primaries = opts.Primaries
	}
	if opts.Workers > 0 {
		lnarwhal.Workers = opts.Workers
	}
	if opts.Out != nil {
		lnarwhal.Out = opts.Out
	}

	if opts.StartFrom == "" {
		tb.Log(nowTS(), "setting up narwhal filesystem...")
		require.NoError(tb, lnarwhal.SetupFS(ctx, opts.Start))
	}

	tb.Log(nowTS(), "starting narwhal nodes...")
	narwhalStartFn := lnarwhal.Start
	if opts.StartFrom != "" {
		narwhalStartFn = func(ctx context.Context) error {
			return lnarwhal.StartFrom(ctx, opts.StartFrom)
		}
	}
	require.NoError(tb, narwhalStartFn(ctx))
	tb.Logf("%s running narwhal node config: batch_size=%d header_size=%d primaries=%d workers=%d",
		nowTS(), lnarwhal.BatchSize, lnarwhal.HeaderSize, lnarwhal.Primaries, lnarwhal.Workers,
	)

	return &lnarwhal
}

func startDefaultTMNodes(ctx context.Context, tb testing.TB, tmOpts []narwhalmint.TMOpts, opts narwhalTMOpts) *narwhalmint.LauncherTendermint {
	tb.Helper()

	if opts.Start.IsZero() {
		opts.Start = time.Now()
	}

	ltm := narwhalmint.LauncherTendermint{
		Host:                  "localhost",
		LogLevel:              opts.LogLvl,
		ProxyAppType:          "noop",
		RunValidatorInProcess: opts.RunTMInProcess,
	}
	if opts.ProxyAppType != "" {
		ltm.ProxyAppType = opts.ProxyAppType
	}
	if opts.Out != nil {
		ltm.Out = opts.Out
	}

	if opts.StartFrom == "" {
		tb.Log(nowTS(), "setting up tendermint filesystem...")
		require.NoError(tb, ltm.SetupFS(opts.Start, tmOpts))
	}

	tb.Logf("%s starting tendermint nodes with proxy app %s...", nowTS(), ltm.ProxyAppType)
	tmStartFn := ltm.Start
	if opts.StartFrom != "" {
		tmStartFn = func(ctx context.Context) error {
			return ltm.StartFrom(ctx, opts.StartFrom)
		}
	}
	require.NoError(tb, tmStartFn(ctx))
	wait(tb, 5, "tendermint nodes to ready...")

	return &ltm
}

type clientErrs struct {
	name  string
	errs  map[string]int
	total int
}

func (c *clientErrs) errMsgs() []string {
	out := make([]string, 0, len(c.errs))
	for errMsg, count := range c.errs {
		out = append(out, fmt.Sprintf("count: %d\terr: %s", count, errMsg))
	}
	errCount := func(in string) (int, string) {
		parts := strings.SplitN(in, "\t", 2)
		rawCount := strings.TrimPrefix(parts[0], "count: ")
		i, _ := strconv.Atoi(rawCount)
		return i, parts[1]
	}
	sort.Slice(out, func(i, j int) bool {
		iCount, iErr := errCount(out[i])
		jCount, jErr := errCount(out[j])
		if iCount == jCount {
			return iErr < jErr
		}
		return iCount < jCount
	})
	return out
}

type clientMsg struct {
	curTx  int
	name   string
	errMsg string
	took   time.Duration
}

type tmClientRunner struct {
	maxTxs            int
	maxConcurrent     int
	totalTxsPerClient int
	clients           []*narwhalmint.TMClient

	mStats map[string]*clientStats
	took   time.Duration
}

func newTMClientRunner(clients []*narwhalmint.TMClient, maxTxs, maxConcurrent int) *tmClientRunner {
	totalTxsPerClient := maxTxs / len(clients)
	mStats := make(map[string]*clientStats)
	for _, cl := range clients {
		mStats[cl.NodeName] = &clientStats{
			mErrs:    make(map[string]int),
			totalTxs: totalTxsPerClient,
		}
	}
	return &tmClientRunner{
		maxTxs:            maxTxs,
		maxConcurrent:     maxConcurrent,
		totalTxsPerClient: totalTxsPerClient,
		clients:           clients,
		mStats:            mStats,
	}
}

func (r *tmClientRunner) errs() (map[string]clientErrs, int) {
	var (
		out   = make(map[string]clientErrs)
		total int
	)
	for node, st := range r.mStats {
		if len(st.mErrs) == 0 {
			continue
		}
		cErrs := clientErrs{
			name: node,
			errs: st.mErrs,
		}
		for _, count := range st.mErrs {
			total += count
			cErrs.total += count
		}
		out[node] = cErrs
	}
	return out, total
}

func (r *tmClientRunner) printRunStats(tb testing.TB) {
	tb.Helper()

	stats := r.runtimeStats()
	defer func() {
		tb.Logf("runtime stats:\ttook=%s rate=%0.2f(tx/sec)", stats.Took.Truncate(truncTo), stats.RateTxsPerSec)

		for _, cls := range stats.ClientStats {
			curTx, totalErrs, progress := r.mStats[cls.Name].stats()
			part := fmt.Sprintf(
				"\t%s: { cur_tx: %d, progress: %0.2f%%, txs_successful: %s%%, errs: %d, rate_tx_per_sec: %0.2f, took: %s }",
				cls.Name, curTx, progress, cls.PercentSuccessful, totalErrs, cls.RateTxsPerSec, cls.Took.Truncate(truncTo),
			)
			tb.Log(part)
		}
	}()

	if stats.TotalErrs == 0 {
		tb.Log("runner encountered no errors in submitting txs")
		return
	}

	tb.Logf("runner clients encountered %d errors: ", stats.TotalErrs)
	for _, clSt := range stats.ClientStats {
		if clSt.TotalErrs == 0 {
			continue
		}
		tb.Logf("\tnode %s total errors: %d", clSt.Name, clSt.TotalErrs)
		for _, err := range clSt.Errs {
			tb.Logf("\t\t%d: %s", err.Count, err.Msg)
		}
	}

}

type tmNodeStatus struct {
	name       string
	status     *coretypes.ResultStatus
	statusErr  error
	netInfo    *coretypes.ResultNetInfo
	netInfoErr error
}

func (r *tmClientRunner) printSyncInfo(ctx context.Context, tb testing.TB) {
	tb.Helper()

	var (
		names []string
		parts = make(map[string]string)
	)
	for _, st := range r.nodeStatuses(ctx) {
		curTx, totalErrs, progress := r.mStats[st.name].stats()
		part := fmt.Sprintf("%s: { cur_tx: %d, progress: %0.3f%%, errs: %d, ", st.name, curTx, progress, totalErrs)
		if st.statusErr != nil {
			part += fmt.Sprintf("status_err: %s, ", st.statusErr.Error())
		} else {
			si := st.status.SyncInfo
			part += fmt.Sprintf("lbh: %d, ", si.LatestBlockHeight)
		}
		if st.netInfoErr != nil {
			part += fmt.Sprintf("net_info_err: %s", st.netInfoErr.Error())
		} else {
			ni := st.netInfo
			part += fmt.Sprintf("net_info: { peers: %d,  listeners: %v }", ni.NPeers, ni.Listeners)
		}
		parts[st.name] = part + " }"
		names = append(names, st.name)
	}
	if len(parts) == 0 {
		return
	}
	sort.Strings(names)

	var output string
	for _, name := range names {
		if output != "" {
			output += " "
		}
		output += parts[name]
	}
	tb.Log(nowTS() + " " + output)
}

func (r *tmClientRunner) nodeStatuses(ctx context.Context) []tmNodeStatus {
	statusStream := make(chan tmNodeStatus)
	go func(clients []*narwhalmint.TMClient) {
		defer close(statusStream)

		wg := new(sync.WaitGroup)
		for i := range clients {
			wg.Add(1)
			go func(client *narwhalmint.TMClient) {
				defer wg.Done()

				st, statusErr := client.Status(ctx)
				netInfo, netErr := client.NetInfo(ctx)

				statusStream <- tmNodeStatus{
					name:       client.NodeName,
					status:     st,
					statusErr:  statusErr,
					netInfo:    netInfo,
					netInfoErr: netErr,
				}
			}(clients[i])
		}
		wg.Wait()
	}(r.clients)

	var out []tmNodeStatus
	for msg := range statusStream {
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].name < out[j].name
	})
	return out
}

func (r *tmClientRunner) reportNodeStatuses(ctx context.Context, t *testing.T, clients []*narwhalmint.TMClient) {
	t.Helper()

	for _, info := range r.nodeStatuses(ctx) {
		if info.statusErr != nil {
			t.Logf("error obtaining sync info for %s: %s", info.name, info.statusErr)
			continue
		}
		cl := findClientByName(info.name, clients)
		si := info.status.SyncInfo
		start := tmmath.Max(si.EarliestBlockHeight, si.LatestBlockHeight-10)
		if start == 0 {
			t.Log(info.name + " at height zero... skipping")
			continue
		}
		t.Log(info.name)
		for i := start; i <= si.LatestBlockHeight; i++ {
			block, err := cl.Block(ctx, &i)
			require.NoError(t, err)
			require.NotNil(t, block.Block)
			txs, colls := block.Block.Txs, block.Block.Collections

			t.Logf("  height %d for block %s has %d txs and %d collections", i, block.BlockID, len(txs), colls.Count())
			t.Logf("\tblock header: %#v", block.Block.Header)
			if len(txs) == 0 {
				continue
			}
			showTxs := txs
			if len(txs) > 5 {
				showTxs = showTxs[:5]
			}
			t.Logf("\tshowing %d of %d total txs", len(showTxs), len(txs))
			for _, tx := range showTxs {
				t.Logf("\ttx:\t%s", string(tx))
			}
		}
	}
}

func (r *tmClientRunner) watchClientTxSubmissions(ctx context.Context, tb testing.TB, checkDur time.Duration) <-chan struct{} {
	end := make(chan struct{})
	go func() {
		done := r.submitClientTMTxs(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(checkDur):
				r.printSyncInfo(ctx, tb)
			case <-done:
				r.printSyncInfo(ctx, tb)
				close(end) // make sure we print before closing above
				return
			}
		}
	}()
	return end
}

func (r *tmClientRunner) submitClientTMTxs(ctx context.Context) <-chan struct{} {
	start := time.Now()
	done := make(chan struct{})
	msgStream := make(chan clientMsg)

	go func(clients []*narwhalmint.TMClient) {
		defer close(msgStream)

		wg := new(sync.WaitGroup)
		for i := range clients {
			wg.Add(1)
			go func(cl *narwhalmint.TMClient) {
				defer wg.Done()
				r.submitTMTxs(ctx, cl, start, msgStream)
			}(clients[i])
		}
		wg.Wait()
	}(r.clients)

	go func() {
		defer close(done)
		for msg := range msgStream {
			r.applyMsg(msg)
		}
		r.took = time.Since(start)
	}()

	return done
}

func (r *tmClientRunner) applyMsg(msg clientMsg) {
	r.mStats[msg.name].applyMsg(msg)
}

func (r *tmClientRunner) submitTMTxs(ctx context.Context, cl *narwhalmint.TMClient, start time.Time, msgStream chan<- clientMsg) {
	sem := make(chan struct{}, r.maxConcurrent)
	wg := new(sync.WaitGroup)
	maxTxs := r.totalTxsPerClient
	for i := 0; i <= maxTxs; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			tx := types.Tx(fmt.Sprintf("%s:tx-%d", cl.NodeName, i))

			_, err := cl.BroadcastTxAsync(ctx, tx)
			var errMsg string
			if err != nil {
				errMsg = err.Error()
				if strings.Contains(errMsg, "connection reset by peer") {
					parts := strings.Split(errMsg, ": ")
					parts = append(parts[:2], parts[3:]...)
					errMsg = strings.Join(parts, ": ")
				}
			}

			msg := clientMsg{curTx: i, name: cl.NodeName, errMsg: errMsg}
			if i == maxTxs {
				msg.took = time.Since(start)
			}
			select {
			case <-ctx.Done():
				if msg.took == 0 {
					msg.took = time.Since(start)
				}
			case msgStream <- msg:
			}
		}(i)
	}
	wg.Wait()
}

type (
	testStats struct {
		MaxTxs                 int       `json:"max_txs"`
		MaxConcurrent          int       `json:"max_concurrent"`
		RateTxsPerSec          float64   `json:"txs_per_second"`
		Took                   prettyDur `json:"took"`
		TotalErrs              int       `json:"total_errs"`
		TotalPercentSuccessful string    `json:"total_percent_successful"`
		TotalTxsSubmitted      int       `json:"total_txs_submitted"`

		ClientStats []testClientStats `json:"z_client_stats"`
	}

	testClientStats struct {
		Name              string    `json:"name"`
		MaxTxs            int       `json:"max_txs"`
		PercentSuccessful string    `json:"percent_successful"`
		RateTxsPerSec     float64   `json:"txs_per_second"`
		Took              prettyDur `json:"took"`
		TotalErrs         int       `json:"total_errs"`
		TotalTxsSubmitted int       `json:"total_txs_submitted"`

		Errs []testClientErr `json:"z_errs"`
	}

	testClientErr struct {
		Count int    `json:"count"`
		Msg   string `json:"msg"`
	}
)

func (r *tmClientRunner) runtimeStats() testStats {
	stringPerc := func(in float64) string {
		return fmt.Sprintf("%0.2f", math.Abs(in))
	}

	errSt, totalErrs := r.errs()

	out := testStats{
		MaxTxs:        r.maxTxs,
		MaxConcurrent: r.maxConcurrent,
		Took:          prettyDur{r.took},
		TotalErrs:     totalErrs,
	}

	var (
		totalSuccess      float64
		totalAttemptedTxs int
	)
	for node, st := range r.mStats {
		curTx, nTotalErrs, _ := st.stats()
		txsSuccess := (float64(st.totalTxs-nTotalErrs) / float64(st.totalTxs)) * 100
		totalSuccess += txsSuccess
		totalAttemptedTxs += curTx
		nErrs := errSt[node]

		var cErrs []testClientErr
		for errMsg, count := range nErrs.errs {
			cErrs = append(cErrs, testClientErr{Count: count, Msg: errMsg})
		}
		sort.Slice(cErrs, func(i, j int) bool {
			return cErrs[i].Count < cErrs[j].Count
		})

		out.ClientStats = append(out.ClientStats, testClientStats{
			Name:              node,
			MaxTxs:            r.totalTxsPerClient,
			PercentSuccessful: stringPerc(txsSuccess),
			RateTxsPerSec:     float64(curTx) / tmmath.Max(st.took.Seconds(), 0.001),
			Took:              prettyDur{st.took},
			TotalErrs:         nTotalErrs,
			TotalTxsSubmitted: curTx,
			Errs:              cErrs,
		})
	}
	out.RateTxsPerSec = float64(totalAttemptedTxs) / out.Took.Seconds()
	out.TotalPercentSuccessful = stringPerc(totalSuccess / float64(len(r.mStats)))
	sort.Slice(out.ClientStats, func(i, j int) bool {
		return out.ClientStats[i].Name < out.ClientStats[j].Name
	})

	return out
}

type clientStats struct {
	mu        sync.RWMutex
	took      time.Duration
	totalTxs  int
	currentTx int
	mErrs     map[string]int
}

func (c *clientStats) applyMsg(msg clientMsg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.curTx > c.currentTx {
		c.currentTx = msg.curTx
	}
	if msg.took > 0 {
		c.took = msg.took
	}
	if msg.errMsg == "" {
		return
	}
	c.mErrs[msg.errMsg]++
}

func (c *clientStats) stats() (currentTx, totalErrs int, progress float64) {
	c.mu.RLock()
	{
		currentTx = c.currentTx
		progress = float64(currentTx) / float64(c.totalTxs)
		for _, count := range c.mErrs {
			totalErrs += count
		}
	}
	c.mu.RUnlock()
	return currentTx, totalErrs, progress * 100
}

type prettyDur struct {
	time.Duration
}

func (p prettyDur) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Duration.Truncate(truncTo).String())
}

func (p *prettyDur) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	p.Duration = dur

	return nil
}

func writeTestStats(tb testing.TB, testdir string, start time.Time, stats testStats) {
	tb.Helper()

	filename := filepath.Join(testdir, "testrun_stats_"+narwhalmint.TimeFilepath(start)+".json")
	f, err := os.Create(filename)
	require.NoError(tb, err)
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	require.NoError(tb, enc.Encode(stats))
}

func findClientByName(name string, clients []*narwhalmint.TMClient) *narwhalmint.TMClient {
	for _, cl := range clients {
		if cl.NodeName == name {
			return cl
		}
	}
	return nil
}

func nowTS() string {
	return time.Now().Format(time.StampMilli)
}

func getEnvOrDefault(env string, def int) int {
	i, err := strconv.Atoi(strings.TrimSpace(os.Getenv(env)))
	if err != nil {
		return def
	}
	return i
}

func dirExists(dir string) bool {
	_, err := os.Stat(dir)
	return err == nil
}
