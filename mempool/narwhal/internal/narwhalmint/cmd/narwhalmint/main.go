package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalmint"
)

func main() {
	cmd := newCMD()

	err := cmd.ExecuteContext(context.Background())
	if err != nil {
		os.Exit(1)
	}
}

func newCMD() *cobra.Command {
	return new(builder).cmd()
}

type builder struct {
	outputDir                 string
	batchSize                 int
	batchDelay                time.Duration
	chainID                   string
	checkDur                  time.Duration
	follow                    bool
	followDur                 time.Duration
	headerSize                int
	headerDelay               time.Duration
	host                      string
	json                      bool
	logFmt                    string
	logLevel                  string
	maxConcurrency            int
	maxTxs                    int
	narwhalPrimaryMetricsPort int
	narwhalWorkerMetricsPort  int
	p2pPort                   string
	primaries                 int
	proxyApp                  string
	reapDur                   time.Duration
	rpcPort                   string
	tmMetricsPort             int
	txSize                    int
	workers                   int
}

func (b *builder) cmd() *cobra.Command {
	const cliName = "narwhalmint"
	cmd := cobra.Command{
		Use: cliName,
	}
	b.registerHostFlag(&cmd)
	b.registerNarwhalConfigFlags(&cmd)
	b.registerOutputFlag(&cmd)
	b.registerTMFlags(&cmd)
	cmd.Flags().IntVar(&b.primaries, "narwhal-primaries", 4, "number of narwhal primaries")

	cmd.AddCommand(
		completionCmd(cliName),
		b.cmdConfigGen(),
		b.cmdLoad(),
		b.cmdStats(),
		b.cmdPromConfig(),
	)

	return &cmd
}

func (b *builder) cmdConfigGen() *cobra.Command {
	cmd := cobra.Command{
		Use:   "config-gen",
		Args:  cobra.NoArgs,
		RunE:  b.configGenRunE,
		Short: "create configuration for a remote cluster",
	}
	b.registerHostFlag(&cmd)
	b.registerNarwhalConfigFlags(&cmd)
	b.registerOutputFlag(&cmd)
	b.registerTMFlags(&cmd)
	cmd.Flags().StringVar(&b.p2pPort, "p2p-port", "26656", "p2p port to listen on; defaults to randomly assigned")
	cmd.Flags().StringVar(&b.rpcPort, "rpc-port", "26657", "rpc port to liste on; defaults to randomly assigned")

	return &cmd
}

func (b *builder) configGenRunE(cmd *cobra.Command, _ []string) error {
	bb, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return err
	}

	var ips []struct {
		ExternalIP string `json:"external_ip"`
		InternalIP string `json:"internal_ip"`
		Type       string `json:"node_type"`
	}
	err = json.Unmarshal(bb, &ips)
	if err != nil {
		return err
	}

	mValidatorIPs := make(map[string]string)
	mSeedIPs := make(map[string]string)
	for _, ip := range ips {
		m := mValidatorIPs
		if ip.Type == "seed" {
			m = mSeedIPs
		}
		m[ip.ExternalIP] = ip.InternalIP
	}

	var opts []narwhalmint.NarwhalOpt
	for externalIP, internalIP := range mValidatorIPs {
		opt := narwhalmint.NarwhalOpt{
			NodeName: internalIP,
			PrimHost: externalIP,
			PrimPort: "54192",
			GRPCHost: internalIP,
			GRPCPort: "54193",
			PromHost: internalIP,
			PromPort: "54194",
			WorkerOpts: []narwhalmint.NarwhalWorkerOpt{{
				PromHost:   internalIP,
				PromPort:   "54195",
				TxsHost:    internalIP,
				WorkerHost: externalIP,
				TxsPort:    "54196",
				WorkerPort: "54197",
			}},
		}
		if b.narwhalPrimaryMetricsPort > -1 {
			opt.PromPort = strconv.Itoa(b.narwhalPrimaryMetricsPort)
		}
		if b.narwhalWorkerMetricsPort > -1 {
			opt.WorkerOpts[0].PromPort = strconv.Itoa(b.narwhalWorkerMetricsPort)
		}
		opts = append(opts, opt)
	}

	var seedIPs []string
	for extIP := range mSeedIPs {
		seedIPs = append(seedIPs, extIP)
	}

	ltm, lnarwhal := b.newLaunchers(cmd.OutOrStdout(), seedIPs)
	err = b.setupFS(cmd.Context(), time.Time{}, ltm, lnarwhal, b.p2pPort, b.rpcPort, opts...)
	if err != nil {
		return err
	}

	var renameOpts []narwhalmint.RenameOpt
	for _, opt := range opts {
		host := opt.PrimHost
		renameOpts = append(renameOpts, narwhalmint.RenameOpt{
			ExternalIP: host,
			Name:       mValidatorIPs[host],
		})
	}
	for extIP, intIP := range mSeedIPs {
		renameOpts = append(renameOpts, narwhalmint.RenameOpt{
			ExternalIP: extIP,
			Name:       intIP,
		})
	}

	err = lnarwhal.RenameDirs(opts...)
	if err != nil {
		return err
	}

	err = ltm.RenameDirs(renameOpts...)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		ChainID    string            `json:"chain_id"`
		Validators map[string]string `json:"validators"`
		Seeds      map[string]string `json:"seeds"`
	}{
		ChainID:    ltm.ChainID(),
		Validators: mValidatorIPs,
		Seeds:      mSeedIPs,
	})
}

func (b *builder) newLaunchers(out io.Writer, seedIPs []string) (*narwhalmint.LauncherTendermint, *narwhalmint.LauncherNarwhal) {
	lnarwhal := narwhalmint.LauncherNarwhal{
		BatchSize:   b.batchSize,
		BatchDelay:  b.batchDelay,
		HeaderSize:  b.headerSize,
		HeaderDelay: b.headerDelay,
		Host:        b.host,
		OutputDir:   b.outputDir,
		Primaries:   b.primaries,
		Workers:     b.workers,
		Out:         out,
	}

	ltm := narwhalmint.LauncherTendermint{
		Host:         b.host,
		LogLevel:     b.logLevel,
		LogFmt:       b.logFmt,
		OutputDir:    b.outputDir,
		ProxyAppType: b.proxyApp,
		ReapDuration: b.reapDur,
		Out:          out,
		SeedIPs:      seedIPs,
	}
	if b.tmMetricsPort >= 0 {
		ltm.MetricsPort = fmt.Sprintf(":%d", b.tmMetricsPort)
	}

	return &ltm, &lnarwhal
}

func (b *builder) setupFS(
	ctx context.Context,
	ts time.Time,
	ltm *narwhalmint.LauncherTendermint,
	lnarwhal *narwhalmint.LauncherNarwhal,
	p2pPort, rpcPort string,
	opts ...narwhalmint.NarwhalOpt,
) error {
	err := lnarwhal.SetupFS(ctx, ts, opts...)
	if err != nil {
		return err
	}

	return ltm.SetupFS(time.Time{}, lnarwhal.TMOpts(p2pPort, rpcPort))
}

func (b *builder) registerHostFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&b.host, "host", "127.0.0.1", "the host; defaults local listen addr")
}

func (b *builder) registerNarwhalConfigFlags(cmd *cobra.Command) {
	b.registerNarwhalMetricsPort(cmd)
	cmd.Flags().IntVar(&b.batchSize, "batch-size", 1<<14, "size of narwhal worker batches in bytes")
	cmd.Flags().DurationVar(&b.batchDelay, "batch-delay", 200*time.Millisecond, "max delay for batches to aggregate")
	cmd.Flags().IntVar(&b.headerSize, "header-size", 1<<8, "narwhal worker batches per header")
	cmd.Flags().DurationVar(&b.headerDelay, "header-delay", time.Second, "max delay for collections to aggregate")
	cmd.Flags().IntVar(&b.workers, "narwhal-workers", 1, "number of narwhal workers per primary")
}

func (b *builder) registerOutputFlag(cmd *cobra.Command) {
	const name = "output"
	cmd.Flags().StringVar(&b.outputDir, name, "", "output dir to put configs; outputs to current working dir")
	cobra.MarkFlagRequired(cmd.Flags(), name)
	cobra.MarkFlagDirname(cmd.Flags(), name)
}

func (b *builder) registerTMFlags(cmd *cobra.Command) {
	b.registerTMMetricsPort(cmd)
	cmd.Flags().StringVar(&b.proxyApp, "proxy-app", "persistent_kvstore", "TM proxy app")
	cmd.Flags().StringVar(&b.logLevel, "log-level", "", "log level for TM nodes; defaults to info")
	cmd.Flags().DurationVar(&b.reapDur, "max-reap-duration", 15*time.Second, "maximum time to wait for reaping the next block to complete")
	cmd.Flags().StringVar(&b.logFmt, "log-format", "plain", "format of tendermint logs; either json or plain; defaults to plain")
}

func (b *builder) registerTMMetricsPort(cmd *cobra.Command) {
	cmd.Flags().IntVar(&b.tmMetricsPort, "tm-metrics-port", -1, "port prom metrics can be scraped")
}

func (b *builder) registerNarwhalMetricsPort(cmd *cobra.Command) {
	cmd.Flags().IntVar(&b.narwhalPrimaryMetricsPort, "narwhal-primary-metrics-port", -1, "port to scrape narwhal metrics")
	cmd.Flags().IntVar(&b.narwhalWorkerMetricsPort, "narwhal-worker-metrics-port", -1, "port to scrape narwhal metrics")
}
