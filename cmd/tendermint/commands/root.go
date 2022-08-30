package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/tendermint/tendermint/cmd/tendermint/commands/debug"
	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/cli"
	tmflags "github.com/tendermint/tendermint/libs/cli/flags"
	"github.com/tendermint/tendermint/libs/log"
	nm "github.com/tendermint/tendermint/node"
)

// WithNodeFunc provides a hook to change the node fn for users wishing to:
//	* Use an external signer for their validators
//	* Supply an in-proc abci app
//	* Supply a genesis doc file from another source
//	* Provide their own DB implementation
// can copy this file and use something other than the DefaultNewNode function
func WithNodeFunc(nodeFn nm.Provider) func(*builderRoot) {
	return func(root *builderRoot) {
		root.nodeFunc = nodeFn
	}
}

// Cmd provides a full tendermint executable.
func Cmd(opts ...func(*builderRoot)) cli.Executor {
	b := builderRoot{
		cfg:      cfg.DefaultConfig(),
		logger:   log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
		nodeFunc: nm.DefaultNewNode,
	}
	for _, o := range opts {
		o(&b)
	}

	defaultHome := os.ExpandEnv(filepath.Join("$HOME", cfg.DefaultTendermintDir))
	return cli.PrepareBaseCmd(b.cmd(), "TM", defaultHome)
}

type builderRoot struct {
	cfg    *cfg.Config
	logger log.Logger

	nodeFunc nm.Provider
}

func (b *builderRoot) cmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "tendermint",
		Short: "BFT state machine replication for applications in any programming languages",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) (err error) {
			if cmd.Name() == versionCmdName {
				return nil
			}

			config, err := parseConfig()
			if err != nil {
				return err
			}
			b.cfg = config

			if config.LogFormat == cfg.LogFormatJSON {
				b.logger = log.NewTMJSONLogger(log.NewSyncWriter(os.Stdout))
			}

			b.logger, err = tmflags.ParseLogLevel(b.cfg.LogLevel, b.logger, cfg.DefaultLogLevel)
			if err != nil {
				return err
			}

			if viper.GetBool(cli.TraceFlag) {
				b.logger = log.NewTracingLogger(b.logger)
			}

			b.logger = b.logger.With("module", "main")
			return nil
		},
	}

	cmd.PersistentFlags().String("log_level", b.cfg.LogLevel, "log level")

	cmd.AddCommand(
		cli.NewCompletionCmd(&cmd, true),
		debug.Cmd(b.logger),
		b.genNodeCmd(),
		b.genValidatorCmd(),
		b.initCmd(),
		b.lightCmd(),
		b.nodeCmd(),
		b.probeUpnpCmd(),
		b.replayCmd(),
		b.replayConsoleCmd(),
		b.resetCmd(),
		b.resetPrivValidatorCmd(),
		b.roolbackStateCmd(),
		b.showNodeCmd(),
		b.showValidatorCmd(),
		b.testnetCmd(),
		b.versionCmd(),
	)

	return &cmd
}

// deprecateSnakeCase is a util function for 0.34.1. Should be removed in 0.35
func deprecateSnakeCase(cmd *cobra.Command, args []string) {
	if strings.Contains(cmd.CalledAs(), "_") {
		fmt.Println("Deprecated: snake_case commands will be replaced by hyphen-case commands in the next major release")
	}
}

// parseConfig retrieves the default environment configuration,
// sets up the Tendermint root and ensures that the root exists
func parseConfig() (*cfg.Config, error) {
	conf := cfg.DefaultConfig()
	err := viper.Unmarshal(conf)
	if err != nil {
		return nil, err
	}
	conf.SetRoot(conf.RootDir)
	cfg.EnsureRoot(conf.RootDir)
	if err := conf.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("error in config file: %v", err)
	}
	return conf, nil
}
