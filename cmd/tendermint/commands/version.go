package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/version"
)

const versionCmdName = "version"

func (b *builderRoot) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   versionCmdName,
		Short: "Show version info",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.TMCoreSemVer)
		},
	}
}
