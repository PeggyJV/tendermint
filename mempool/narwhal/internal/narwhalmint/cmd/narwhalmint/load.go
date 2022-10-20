package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gosuri/uiprogress"
	"github.com/gosuri/uiprogress/util/strutil"
	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalmint"
	tmhttp "github.com/tendermint/tendermint/rpc/client/http"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

func (b *builder) cmdLoad() *cobra.Command {
	cmd := cobra.Command{
		Use:   "load",
		RunE:  b.loadRunE,
		Short: "send load to TM nodes; sends at a continuous load",
		Example: `# piping node ips in
echo '["35.223.226.153", ...OTHER_IPS]' |  narwhalmint load

# setting max concurrency and max txs
<GET_INPUT_IP_CMD> |  narwhalmint load --concurrency 5 --txs 500000
`,
	}
	cmd.Flags().IntVar(&b.maxConcurrency, "concurrency", 1, "maximum client concurrency (i.e. max concurrent load on a single node)")
	cmd.Flags().IntVar(&b.maxTxs, "txs", 1<<10, "maximum number of total txs to be sent; is split evenly between nodes")

	return &cmd
}

func (b *builder) loadRunE(cmd *cobra.Command, args []string) error {
	clients, err := getClientsFromStdIn(cmd.InOrStdin())
	if err != nil {
		return err
	}

	if len(clients) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no ip addresses provided")
		return nil
	}

	runner := newTMClientRunner(cmd.ErrOrStderr(), clients, b.maxTxs, b.maxConcurrency)
	runner.println(fmt.Sprintf("submitting client Txs: max_txs=%d max_concurrent=%d txs/client: %d", runner.maxTxs, runner.maxConcurrent, runner.totalTxsPerClient))

	ctx := cmd.Context()
	select {
	case <-ctx.Done():
	case <-runner.watchClientTxSubmissions(ctx):
	}

	return nil
}

type clientMsg struct {
	curTx  int
	name   string
	errMsg string
	took   time.Duration
}

type tmClientRunner struct {
	w                 io.Writer
	progress          *uiprogress.Progress
	maxTxs            int
	maxConcurrent     int
	totalTxsPerClient int
	clients           []*narwhalmint.TMClient

	mu         sync.Mutex
	mSyncStats map[string]tmNodeStatus

	mStats map[string]*clientStats
	took   time.Duration
}

func newTMClientRunner(w io.Writer, clients []*narwhalmint.TMClient, maxTxs, maxConcurrent int) *tmClientRunner {
	progress := uiprogress.New()
	progress.RefreshInterval = 100 * time.Millisecond
	progress.Out = w
	totalTxsPerClient := maxTxs / len(clients)
	mStats := make(map[string]*clientStats)
	for _, cl := range clients {
		mStats[cl.NodeName] = &clientStats{
			mErrs:    make(map[string]int),
			totalTxs: totalTxsPerClient,
		}
	}
	return &tmClientRunner{
		w:                 w,
		progress:          progress,
		maxTxs:            maxTxs,
		maxConcurrent:     maxConcurrent,
		totalTxsPerClient: totalTxsPerClient,
		clients:           clients,
		mStats:            mStats,
		mSyncStats:        make(map[string]tmNodeStatus),
	}
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
					Name:       client.NodeName,
					Status:     st,
					StatusErr:  statusErr,
					NetInfo:    netInfo,
					NetInfoErr: netErr,
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
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *tmClientRunner) watchClientTxSubmissions(ctx context.Context) <-chan struct{} {
	end := make(chan struct{})
	go func() {
		done := r.submitClientTMTxs(ctx)
		sem := make(chan struct{}, 1)
		updateStatuses := func(ctx context.Context) {
			select {
			case sem <- struct{}{}:
				go func() {
					defer func() { <-sem }()
					r.updateSyncStats(ctx)
				}()
			default:
			}
		}

		updateStatuses(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				updateStatuses(ctx)
			case <-done:
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
	r.progress.Start()

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
		defer r.progress.Stop()
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
	st := r.clientRunStats(cl.NodeName)
	bar := r.progress.AddBar(maxTxs).
		AppendFunc(func(b *uiprogress.Bar) string {
			lbh, _ := r.clientLBH(cl.NodeName)
			peers := r.clientPeers(cl.NodeName)
			return fmt.Sprintf("%s %s lbh(%05d) peers(%02d) errs(%d)",
				b.CompletedPercentString(),
				b.TimeElapsedString(),
				lbh,
				peers,
				st.totalErrs(),
			)
		}).
		PrependFunc(func(b *uiprogress.Bar) string {
			completed := fmt.Sprintf("%d / %d", st.currentTx, st.totalTxs)
			completed = strutil.PadLeft(completed, 18, ' ')
			return fmt.Sprintf("    %s %s", strutil.Resize("node "+cl.NodeName, 20), completed)
		})

	for i := 0; i <= maxTxs; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			bar.Incr()
			tx := types.Tx(fmt.Sprintf("%s:tx-%d-%d", cl.NodeName, i, time.Now().Unix()))

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

func (r *tmClientRunner) updateSyncStats(ctx context.Context) {
	statuses := r.nodeStatuses(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, st := range statuses {
		st := st
		r.mSyncStats[st.Name] = st
	}
}

func (r *tmClientRunner) println(args ...any) {
	fmt.Fprintln(r.w, args...)
}

func (r *tmClientRunner) clientRunStats(name string) *clientStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mStats[name]
}

func (r *tmClientRunner) clientLBH(name string) (int64, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.mSyncStats[name].lbs()
}

func (r *tmClientRunner) clientPeers(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.mSyncStats[name].peers()
}

type tmNodeStatus struct {
	Name       string                   `json:"node_name"`
	Status     *coretypes.ResultStatus  `json:"status"`
	StatusErr  error                    `json:"status_err,omitempty"`
	NetInfo    *coretypes.ResultNetInfo `json:"net_info"`
	NetInfoErr error                    `json:"net_info_err,omitempty"`
}

func (t tmNodeStatus) lbs() (int64, time.Time) {
	if t.Status == nil {
		return 0, time.Time{}
	}
	return t.Status.SyncInfo.LatestBlockHeight, t.Status.SyncInfo.LatestBlockTime
}

func (t tmNodeStatus) peers() int {
	if t.NetInfo == nil {
		return 0
	}
	return t.NetInfo.NPeers
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
	}
	c.mu.RUnlock()
	return currentTx, c.totalErrs(), progress * 100
}

func (c *clientStats) totalErrs() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var totalErrs int
	for _, count := range c.mErrs {
		totalErrs += count
	}
	return totalErrs
}

func getClientsFromStdIn(r io.Reader) ([]*narwhalmint.TMClient, error) {
	bb, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var ipAddrs []string
	err = json.Unmarshal(bb, &ipAddrs)
	if err != nil {
		return nil, err
	}
	sort.Strings(ipAddrs)

	var clients []*narwhalmint.TMClient
	for _, ip := range ipAddrs {
		httpc, err := tmhttp.NewWithTimeout("tcp://"+ip+":26657", "/websockets", 15)
		if err != nil {
			return nil, err
		}
		clients = append(clients, &narwhalmint.TMClient{
			NodeName: ip,
			HTTP:     httpc,
		})
	}

	return clients, nil
}
