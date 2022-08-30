package commands

import (
	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/consensus"
)

func (b *builderRoot) replayCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "replay",
		Short: "Replay messages from WAL",
		Run: func(cmd *cobra.Command, args []string) {
			consensus.RunReplayFile(b.cfg.BaseConfig, b.cfg.Consensus, false)
		},
	}

	return &cmd
}

// ReplayConsoleCmd allows replaying of messages from the WAL in a
// console.
func (b *builderRoot) replayConsoleCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:     "replay-console",
		Aliases: []string{"replay_console"},
		Short:   "Replay messages from WAL in a console",
		Run: func(cmd *cobra.Command, args []string) {
			consensus.RunReplayFile(b.cfg.BaseConfig, b.cfg.Consensus, true)
		},
		PreRun: deprecateSnakeCase,
	}

	return &cmd
}
