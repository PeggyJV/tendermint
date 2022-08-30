package debug

import (
	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/libs/log"
)

func Cmd(logger log.Logger) *cobra.Command {
	b := builder{
		logger: logger,
	}
	return b.cmd()
}

type builder struct {
	logger log.Logger

	nodeRPCAddr string
	profAddr    string
	frequency   uint
}

func (b *builder) cmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "debug",
		Short: "A utility to kill or watch a Tendermint process while aggregating debugging data",
	}

	cmd.PersistentFlags().SortFlags = true
	cmd.PersistentFlags().StringVar(
		&b.nodeRPCAddr,
		"rpc-laddr",
		"tcp://localhost:26657",
		"the Tendermint node's RPC address (<host>:<port>)",
	)

	cmd.AddCommand(
		b.killCmd(),
		b.dumpCmd(),
	)

	return &cmd
}
