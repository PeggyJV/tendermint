package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosuri/uiprogress"
	"github.com/gosuri/uiprogress/util/strutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/libs/math"
	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalmint"
	tmhttp "github.com/tendermint/tendermint/rpc/client/http"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
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
	cmd.Flags().IntVar(&b.txSize, "tx-size", 0, "size of tx")
	cmd.Flags().StringVar(&b.chainID, "chain-id", "", "chain id of the chain under load")
	cobra.MarkFlagRequired(cmd.Flags(), "chain-id")

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

	runner := newTMClientRunner(cmd.ErrOrStderr(), clients, b.maxTxs, b.maxConcurrency, b.txSize)
	runner.println(fmt.Sprintf("submitting client Txs: max_txs=%d max_concurrent=%d txs/client: %d", runner.maxTxs, runner.maxConcurrent, runner.totalTxsPerClient))

	pusher, err := b.pushLoadMetrics(len(clients), runner.totalTxsPerClient)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "failed to push load metrics: "+err.Error())
	}
	defer pusher.Delete()

	ctx := cmd.Context()
	select {
	case <-ctx.Done():
	case <-runner.watchClientTxSubmissions(ctx):
	}

	return nil
}

func (b *builder) pushLoadMetrics(numClients, maxTxsPerClient int) (*push.Pusher, error) {
	gaugeFn := func(name string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "observability",
			Subsystem: "loadtest",
			Name:      name,
			ConstLabels: prometheus.Labels{
				"chain_id":               b.chainID,
				"max_client_concurrency": strconv.Itoa(b.maxConcurrency),
				"max_client_txs":         strconv.Itoa(maxTxsPerClient),
				"max_txs":                strconv.Itoa(b.maxTxs),
			},
		})
	}
	clientsGauge := gaugeFn("clients")
	clientsGauge.Set(float64(numClients))

	txSizeGauge := gaugeFn("tx_size")
	txSizeGauge.Set(float64(b.txSize))

	pusher := push.New("localhost:9091", "prompush").
		Collector(clientsGauge).
		Collector(txSizeGauge)

	return pusher, pusher.Push()
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

	starter chan chan struct{}
	took    time.Duration
	txSize  int
}

func newTMClientRunner(w io.Writer, clients []*narwhalmint.TMClient, maxTxs, maxConcurrent, txSize int) *tmClientRunner {
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
		starter:           make(chan chan struct{}),
		txSize:            txSize,
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

	afterReadyFn, err := r.waitForReady(ctx)
	if err != nil {
		panic("unexpected failure to prepare: " + err.Error())
	}
	r.progress.Start()
	afterReadyFn()

	return done
}

func (r *tmClientRunner) applyMsg(msg clientMsg) {
	r.mStats[msg.name].applyMsg(msg)
}

func (r *tmClientRunner) waitForReady(ctx context.Context) (func(), error) {
	var readyChans []chan struct{}
	for i := 0; i < len(r.clients); i++ {
		readyChan := make(chan struct{})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r.starter <- readyChan:
			readyChans = append(readyChans, readyChan)
		}
	}

	return func() {
		for _, readyChan := range readyChans {
			close(readyChan)
		}
	}, nil
}

func (r *tmClientRunner) submitTMTxs(ctx context.Context, cl *narwhalmint.TMClient, start time.Time, msgStream chan<- clientMsg) {
	sem := make(chan struct{}, r.maxConcurrent)
	wg := new(sync.WaitGroup)
	maxTxs := r.totalTxsPerClient
	st := r.clientRunStats(cl.NodeName)
	bar := r.progress.AddBar(maxTxs).
		AppendFunc(func(b *uiprogress.Bar) string {
			lbh, lbt := r.clientLBH(cl.NodeName)
			peers := r.clientPeers(cl.NodeName)
			return fmt.Sprintf("%s %s lbh(%05d) lbt(%s) peers(%02d) errs(%d)",
				b.CompletedPercentString(),
				b.TimeElapsedString(),
				lbh,
				lbtTimestamp(lbt),
				peers,
				st.totalErrs(),
			)
		}).
		PrependFunc(func(b *uiprogress.Bar) string {
			completed := fmt.Sprintf("%d / %d", st.currentTx, st.totalTxs)
			completed = strutil.PadLeft(completed, 18, ' ')
			return fmt.Sprintf("    %s %s", strutil.Resize("node "+cl.NodeName, 20), completed)
		})

	select {
	case <-ctx.Done():
		return
	case <-<-r.starter:
	}

	for i := 0; i <= maxTxs; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			bar.Incr()

			txNum, epoch := strconv.Itoa(i), strconv.FormatInt(time.Now().Unix(), 10)
			padding := math.Max(r.txSize-len(cl.NodeName)-len(txNum)-len(epoch), 1)

			tx := struct {
				NodeName string `json:"node_name"`
				TxNum    string `json:"tx_num"`
				Epoch    string `json:"epoch"`
				Padding  []byte `json:"padding"`
			}{
				NodeName: cl.NodeName,
				TxNum:    txNum,
				Epoch:    epoch,
				Padding:  make([]byte, padding),
			}

			var (
				errMsg  string
				txBytes []byte
			)

			if _, err := rand.Read(tx.Padding); err != nil {
				errMsg = err.Error()
			} else {
				txBytes, err = json.Marshal(tx)
				if err != nil {
					errMsg = err.Error()
				}
			}

			if len(txBytes) > 0 && errMsg == "" {
				_, err := cl.BroadcastTxAsync(ctx, txBytes)
				if err != nil {
					errMsg = err.Error()
					if strings.Contains(errMsg, "connection reset by peer") {
						parts := strings.Split(errMsg, ": ")
						parts = append(parts[:2], parts[3:]...)
						errMsg = strings.Join(parts, ": ")
					}
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
	ipAddrs, err := getIPsFromStdIn(r)
	if err != nil {
		return nil, err
	}

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

func getIPsFromStdIn(r io.Reader) ([]string, error) {
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

	return ipAddrs, nil
}
