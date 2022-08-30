package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	tmjson "github.com/tendermint/tendermint/libs/json"
	"github.com/tendermint/tendermint/privval"
)

func (b *builderRoot) genValidatorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "gen-validator",
		Aliases: []string{"gen_validator"},
		Short:   "Generate new validator keypair",
		PreRun:  deprecateSnakeCase,
		RunE:    genValidator,
	}
}

func genValidator(cmd *cobra.Command, args []string) error {
	pv := privval.GenFilePV("", "")
	jsbz, err := tmjson.Marshal(pv)
	if err != nil {
		panic(err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), `%v
`, string(jsbz))
	return err
}
