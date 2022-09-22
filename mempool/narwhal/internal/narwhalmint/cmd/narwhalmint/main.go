package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
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
	outputDir  string
	batchSize  int
	fromDir    string
	headerSize int
	host       string
	p2pPort    string
	primaries  int
	proxyApp   string
	rpcPort    string
	workers    int
}

func (b *builder) cmd() *cobra.Command {
	const cliName = "narwhalmint"
	cmd := cobra.Command{
		Use:   cliName,
		RunE:  b.cmdRunE,
		Short: "start a TM+narwhal cluster",
	}
	b.registerHostFlag(&cmd)
	b.registerNarwhalConfigFlags(&cmd)
	b.registerOutputFlag(&cmd)
	b.registerTMFlags(&cmd)
	cmd.Flags().StringVar(&b.fromDir, "from", "", "restart existing TM cluster")
	cmd.Flags().IntVar(&b.primaries, "narwhal-primaries", 4, "number of narwhal primaries")

	cmd.AddCommand(
		completionCmd(cliName),
		b.cmdConfigGen(),
	)

	return &cmd
}

func (b *builder) cmdRunE(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	if b.fromDir != "" {
		return b.startFrom(ctx, cmd.OutOrStdout())
	}

	w := cmd.OutOrStdout()

	ltm, lnarwhal := b.newLaunchers(w)

	w.Write([]byte("setting up FS...\n"))
	err := b.setupFS(ctx, time.Time{}, ltm, lnarwhal, "", "")
	if err != nil {
		return err
	}

	w.Write([]byte("starting narwhal nodes...\n"))
	err = lnarwhal.Start(ctx)
	if err != nil {
		return err
	}

	w.Write([]byte("starting tendermint nodes...\n"))
	err = ltm.Start(ctx)
	if err != nil {
		return err
	}
	w.Write([]byte("nodes started successfully\n"))

	<-ctx.Done()
	return nil
}

func (b *builder) startFrom(ctx context.Context, w io.Writer) error {
	ltm, lnarwhal := b.newLaunchers(w)

	err := lnarwhal.StartFrom(ctx, b.fromDir)
	if err != nil {
		return err
	}

	return ltm.StartFrom(ctx, b.fromDir)
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
	}
	err = json.Unmarshal(bb, &ips)
	if err != nil {
		return err
	}

	mIPs := make(map[string]string)
	for _, ip := range ips {
		mIPs[ip.ExternalIP] = ip.InternalIP
	}

	var opts []narwhalmint.NarwhalOpt
	for externalIP, internalIP := range mIPs {
		opts = append(opts, narwhalmint.NarwhalOpt{
			NodeName: internalIP,
			PrimHost: externalIP,
			PrimPort: "54192",
			GRPCHost: internalIP,
			GRPCPort: "54193",
			WorkerOpts: []narwhalmint.NarwhalWorkerOpt{{
				TxsHost:    internalIP,
				TxsPort:    "54194",
				WorkerHost: externalIP,
				WorkerPort: "54195",
			}},
		})
	}

	ltm, lnarwhal := b.newLaunchers(cmd.OutOrStdout())
	err = b.setupFS(cmd.Context(), time.Time{}, ltm, lnarwhal, b.p2pPort, b.rpcPort, opts...)
	if err != nil {
		return err
	}

	var renameOpts []narwhalmint.RenameOpt
	for _, opt := range opts {
		host := opt.PrimHost
		renameOpts = append(renameOpts, narwhalmint.RenameOpt{
			ExternalIP: host,
			Name:       mIPs[host],
		})
	}

	err = lnarwhal.RenameDirs(opts...)
	if err != nil {
		return err
	}
	return ltm.RenameDirs(renameOpts...)
}

func (b *builder) newLaunchers(out io.Writer) (*narwhalmint.LauncherTendermint, *narwhalmint.LauncherNarwhal) {
	lnarwhal := narwhalmint.LauncherNarwhal{
		BatchSize:  b.batchSize,
		HeaderSize: b.headerSize,
		Host:       b.host,
		OutputDir:  b.outputDir,
		Primaries:  b.primaries,
		Workers:    b.workers,
		Out:        out,
	}

	ltm := narwhalmint.LauncherTendermint{
		Host:         b.host,
		OutputDir:    b.outputDir,
		ProxyAppType: b.proxyApp,
		Out:          out,
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
	cmd.Flags().IntVar(&b.batchSize, "batch-size", 1<<14, "size of narwhal worker batches in bytes")
	cmd.Flags().IntVar(&b.headerSize, "header-size", 1<<8, "narwhal worker batches per header")
	cmd.Flags().IntVar(&b.workers, "narwhal-workers", 1, "number of narwhal workers per primary")
}

func (b *builder) registerOutputFlag(cmd *cobra.Command) {
	const name = "output"
	cmd.Flags().StringVar(&b.outputDir, name, "", "output dir to put configs; outputs to current working dir")
	cobra.MarkFlagRequired(cmd.Flags(), name)
	cobra.MarkFlagDirname(cmd.Flags(), name)
}

func (b *builder) registerTMFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&b.proxyApp, "proxy-app", "persistent_kvstore", "TM proxy app")
}
