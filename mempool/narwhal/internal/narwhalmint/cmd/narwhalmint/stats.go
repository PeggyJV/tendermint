package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/gosuri/uiprogress"
	"github.com/gosuri/uiprogress/util/strutil"
	"github.com/spf13/cobra"
)

const year = 365 * 24 * time.Hour

func (b *builder) cmdStats() *cobra.Command {
	cmd := cobra.Command{
		Use:   "stats",
		RunE:  b.nodeStatsRunE,
		Short: "check status of nodes",
		Long: `The stats command will poll the nodes by the provided IPs and request
the last block height (lbh), the last block time (lbt), the number of
active peers(peers), and the number of times it checked, and how long
the stats have been viewed for`,
		Example: `#get single status check
echo '["35.223.226.153", ...OTHER_IPS]' |  narwhalmint stats

# follow/tail the statuses indefinitely
<GET_INPUT_IP_CMD> |  narwhalmint stats -f

# follow/tail with check interval override
<GET_INPUT_IP_CMD> |  narwhalmint stats -f --interval 1s`,
	}
	cmd.Flags().BoolVarP(&b.follow, "follow", "f", false, "follow the stats on interval")
	cmd.Flags().DurationVar(&b.checkDur, "interval", time.Second, "interval to check stats when following")
	cmd.Flags().DurationVar(&b.followDur, "for", 0, "duration to follow the stats for; helpful in to set in automated runs")
	cmd.Flags().BoolVar(&b.json, "json", false, "output json in place of the std error output")

	return &cmd
}

func (b *builder) nodeStatsRunE(cmd *cobra.Command, args []string) error {
	clients, err := getClientsFromStdIn(cmd.InOrStdin())
	if err != nil {
		return err
	}

	if len(clients) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no ip addresses provided")
		return nil
	}

	r := newTMClientRunner(cmd.ErrOrStderr(), clients, 0, 0)
	ctx := cmd.Context()

	r.updateSyncStats(ctx)

	incrFn, stopFn := b.newJSONOut(cmd.OutOrStdout(), r)
	if !b.json {
		incrFn, stopFn = b.newProgressBars(cmd.ErrOrStderr(), r)
	}
	defer stopFn()

	incrFn()

	if !b.follow {
		return nil
	}

	stop := newStopTimer(b.followDur)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stop:
			r.updateSyncStats(ctx)
			incrFn()
			return nil
		case <-time.After(b.checkDur):
			r.updateSyncStats(ctx)
			incrFn()
		}
	}
}

func (b *builder) newJSONOut(w io.Writer, r *tmClientRunner) (func(), func()) {
	start := time.Now()
	enc := json.NewEncoder(w)
	incFn := func() {
		summary := struct {
			StartUTC string         `json:"start_time_utc"`
			Statuses []tmNodeStatus `json:"node_status"`
		}{
			StartUTC: start.UTC().Format(time.RFC3339),
		}
		for _, status := range r.mSyncStats {
			summary.Statuses = append(summary.Statuses, status)
		}
		sort.Slice(summary.Statuses, func(i, j int) bool {
			ii, jj := summary.Statuses[i], summary.Statuses[j]
			if ii.Status == nil && jj.Status == nil || jj.Status == nil {
				return true
			}
			return ii.Status != nil && ii.Status.SyncInfo.LatestBlockHeight > jj.Status.SyncInfo.LatestBlockHeight
		})
		enc.Encode(summary)
	}

	stopFn := func() {}

	return incFn, stopFn
}

func (b *builder) newProgressBars(w io.Writer, r *tmClientRunner) (func(), func()) {
	n := 1
	completeFn := func(b *uiprogress.Bar) string {
		return b.CompletedPercentString()
	}
	switch {
	case b.follow && b.followDur == 0:
		n = 20
		completeFn = func(b *uiprogress.Bar) string {
			if b.CompletedPercent() > 94.0 {
				b.Total *= 2
			}
			return fmt.Sprintf("%04d", b.Current())
		}
	case b.follow && b.followDur > 0:
		n = int(b.followDur.Seconds())
		completeFn = func(b *uiprogress.Bar) string {
			b.Set(int(b.TimeElapsed().Seconds()))
			return b.CompletedPercentString()
		}
	}

	refreshInterval := 100 * time.Millisecond

	progress := uiprogress.New()
	progress.SetRefreshInterval(refreshInterval)
	progress.SetOut(w)

	var bars []*uiprogress.Bar
	for _, cl := range r.clients {
		cl := cl
		name := cl.NodeName
		bar := progress.AddBar(n).
			AppendFunc(func(b *uiprogress.Bar) string {
				lbh, lbt := r.clientLBH(cl.NodeName)
				peers := r.clientPeers(cl.NodeName)
				var ts string
				if lbt.After(time.Now().Add(-20 * year)) {
					ts = lbt.Local().Format(time.Kitchen)
				}
				elapsed := strutil.PadLeft(b.TimeElapsedString(), 5, ' ')
				return fmt.Sprintf("%s %s lbh(%05d) lbt(%s) peers(%02d)", completeFn(b), elapsed, lbh, ts, peers)
			}).
			PrependFunc(func(b *uiprogress.Bar) string {
				return "\t" + strutil.Resize("node "+name, 20)
			})
		bars = append(bars, bar)
	}

	incFn := func() {
		incrBars(bars)
	}

	progress.Start()
	stopFn := func() {
		time.Sleep(refreshInterval)
		progress.Stop()
	}

	return incFn, stopFn
}

func incrBars(bars []*uiprogress.Bar) {
	for _, bar := range bars {
		bar.Incr()
	}
}

func newStopTimer(dur time.Duration) <-chan time.Time {
	if dur == 0 {
		return nil
	}
	return time.After(dur)
}
