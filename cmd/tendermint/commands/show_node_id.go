package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/p2p"
)

func (b *builderRoot) showNodeCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:     "show-node-id",
		Aliases: []string{"show_node_id"},
		Short:   "Show this node's ID",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeKey, err := p2p.LoadNodeKey(b.cfg.NodeKeyFile())
			if err != nil {
				return err
			}

			fmt.Println(nodeKey.ID())
			return nil
		},
		PreRun: deprecateSnakeCase,
	}

	return &cmd
}
