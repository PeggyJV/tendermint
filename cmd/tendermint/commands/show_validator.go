package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/config"
	tmjson "github.com/tendermint/tendermint/libs/json"
	tmos "github.com/tendermint/tendermint/libs/os"
	"github.com/tendermint/tendermint/privval"
)

func (b *builderRoot) showValidatorCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:     "show-validator",
		Aliases: []string{"show_validator"},
		Short:   "Show this node's validator info",
		RunE:    showValidatorGen(b.cfg),
		PreRun:  deprecateSnakeCase,
	}

	return &cmd
}

func showValidatorGen(cfg *config.Config) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		keyFilePath := cfg.PrivValidatorKeyFile()
		if !tmos.FileExists(keyFilePath) {
			return fmt.Errorf("private validator file %s does not exist", keyFilePath)
		}

		pv := privval.LoadFilePV(keyFilePath, cfg.PrivValidatorStateFile())

		pubKey, err := pv.GetPubKey()
		if err != nil {
			return fmt.Errorf("can't get pubkey: %w", err)
		}

		bz, err := tmjson.Marshal(pubKey)
		if err != nil {
			return fmt.Errorf("failed to marshal private validator pubkey: %w", err)
		}

		fmt.Println(string(bz))
		return nil
	}
}
