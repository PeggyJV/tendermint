package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	tmos "github.com/tendermint/tendermint/libs/os"
	"github.com/tendermint/tendermint/p2p"
)

func (b *builderRoot) genNodeCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:     "gen-node-key",
		Aliases: []string{"gen_node_key"},
		Short:   "Generate a node key for this node and print its ID",
		PreRun:  deprecateSnakeCase,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeKeyFile := b.cfg.NodeKeyFile()
			if tmos.FileExists(nodeKeyFile) {
				return fmt.Errorf("node key at %s already exists", nodeKeyFile)
			}

			nodeKey, err := p2p.LoadOrGenNodeKey(nodeKeyFile)
			if err != nil {
				return err
			}
			fmt.Println(nodeKey.ID())
			return nil
		},
	}

	return &cmd
}
