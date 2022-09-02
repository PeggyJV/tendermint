package commands

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/cli"
	tmos "github.com/tendermint/tendermint/libs/os"
)

func TestRootHome(t *testing.T) {
	root, newRoot := t.TempDir(), t.TempDir()

	cases := []struct {
		name string
		args []string
		env  map[string]string
		root string
	}{
		{name: "no args or env", root: root},
		{name: "only args", args: []string{"--home", newRoot}, root: newRoot},
		{name: "only env", env: map[string]string{"TMHOME": newRoot}, root: newRoot},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := testSetup(t, root, tc.args, tc.env)

			assert.Equal(t, tc.root, config.RootDir)
			assert.Equal(t, tc.root, config.P2P.RootDir)
			assert.Equal(t, tc.root, config.Consensus.RootDir)
			assert.Equal(t, tc.root, config.Mempool.RootDir)
		})
	}
}

func TestRootFlagsEnv(t *testing.T) {
	// defaults
	defaultLogLvl := cfg.DefaultConfig().LogLevel

	cases := []struct {
		name     string
		args     []string
		env      map[string]string
		logLevel string
	}{
		{name: "wrong flag", args: []string{"--log", "debug"}, logLevel: defaultLogLvl},
		{name: "correct flag", args: []string{"--log_level", "debug"}, logLevel: "debug"},
		{name: "wrong env var", env: map[string]string{"TM_LOW": "debug"}, logLevel: defaultLogLvl},
		{name: "wrong env prefix", env: map[string]string{"MT_LOG_LEVEL": "debug"}, logLevel: defaultLogLvl},
		{name: "right env", env: map[string]string{"TM_LOG_LEVEL": "debug"}, logLevel: "debug"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := testSetup(t, t.TempDir(), tc.args, tc.env)

			assert.Equal(t, tc.logLevel, config.LogLevel)
		})
	}
}

func TestRootConfig(t *testing.T) {
	// write non-default config
	nonDefaultLogLvl := "abc:debug"
	cvals := map[string]string{
		"log_level": nonDefaultLogLvl,
	}

	cases := []struct {
		name string
		args []string
		env  map[string]string

		logLvl string
	}{
		{name: "should load config", logLvl: nonDefaultLogLvl},                                          // should load config
		{name: "flag overrides", args: []string{"--log_level=abc:info"}, logLvl: "abc:info"},            // flag over rides
		{name: "env overrides", env: map[string]string{"TM_LOG_LEVEL": "abc:info"}, logLvl: "abc:info"}, // env over rides
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// XXX: path must match cfg.defaultConfigPath
			configFilePath := filepath.Join(dir, "config")
			err := tmos.EnsureDir(configFilePath, 0700)
			require.Nil(t, err)

			// write the non-defaults to a different path
			// TODO: support writing sub configs so we can test that too
			err = writeConfigVals(configFilePath, cvals)
			require.Nil(t, err)

			config := testSetup(t, dir, tc.args, tc.env)
			require.Nil(t, err)

			assert.Equal(t, tc.logLvl, config.LogLevel)
		})
	}
}

// prepare new rootCmd
func testSetup(t *testing.T, rootDir string, args []string, env map[string]string) *cfg.Config {
	t.Helper()

	var b *builderRoot
	cmd := Cmd(
		func(root *builderRoot) {
			root.runE = func(cmd *cobra.Command, args []string) error {
				return nil
			}
			b = root
		},
		WithHomeDir(rootDir),
	)
	cmd.Flags().String("log", "", "fake log value")

	// run with the args and env
	args = append([]string{cmd.Use}, args...)
	require.NoError(t, cli.RunWithArgs(cmd.Command, args, env))
	return b.cfg
}

// writeConfigVals writes a toml file with the given values.
// It returns an error if writing was impossible.
func writeConfigVals(dir string, vals map[string]string) error {
	data := ""
	for k, v := range vals {
		data += fmt.Sprintf("%s = \"%s\"\n", k, v)
	}
	cfile := filepath.Join(dir, "config.toml")
	return ioutil.WriteFile(cfile, []byte(data), 0600)
}
