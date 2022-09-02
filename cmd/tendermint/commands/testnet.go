package commands

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/bytes"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"
)

const (
	nodeDirPerm = 0755
)

func (b *builderRoot) testnetCmd() *cobra.Command {
	bt := builderTestnet{root: b}
	return bt.cmd()
}

type builderTestnet struct {
	root *builderRoot

	nValidators    int
	nNonValidators int
	initialHeight  int64
	configFile     string
	outputDir      string
	nodeDirPrefix  string

	populatePersistentPeers bool
	hostnamePrefix          string
	hostnameSuffix          string
	startingIPAddress       string
	hostnames               []string
	p2pPort                 int
	randomMonikers          bool
}

func (b *builderTestnet) cmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "testnet",
		Short: "Initialize files for a Tendermint testnet",
		Long: `testnet will create "v" + "n" number of directories and populate each with
necessary files (private validator, genesis, config, etc.).

Note, strict routability for addresses is turned off in the config file.

Optionally, it will fill in persistent_peers list in config file using either hostnames or IPs.

Example:

	tendermint testnet --v 4 --o ./output --populate-persistent-peers --starting-ip-address 192.168.10.2
	`,
		RunE: b.testnetFiles,
	}
	cmd.Flags().IntVar(&b.nValidators, "v", 4,
		"number of validators to initialize the testnet with")
	cmd.Flags().StringVar(&b.configFile, "config", "",
		"config file to use (note some options may be overwritten)")
	cmd.Flags().IntVar(&b.nNonValidators, "n", 0,
		"number of non-validators to initialize the testnet with")
	cmd.Flags().StringVar(&b.outputDir, "o", "./mytestnet",
		"directory to store initialization data for the testnet")
	cmd.Flags().StringVar(&b.nodeDirPrefix, "node-dir-prefix", "node",
		"prefix the directory name for each node with (node results in node0, node1, ...)")
	cmd.Flags().Int64Var(&b.initialHeight, "initial-height", 0,
		"initial height of the first block")

	cmd.Flags().BoolVar(&b.populatePersistentPeers, "populate-persistent-peers", true,
		"update config of each node with the list of persistent peers build using either"+
			" hostname-prefix or"+
			" starting-ip-address")
	cmd.Flags().StringVar(&b.hostnamePrefix, "hostname-prefix", "node",
		"hostname prefix (\"node\" results in persistent peers list ID0@node0:26656, ID1@node1:26656, ...)")
	cmd.Flags().StringVar(&b.hostnameSuffix, "hostname-suffix", "",
		"hostname suffix ("+
			"\".xyz.com\""+
			" results in persistent peers list ID0@node0.xyz.com:26656, ID1@node1.xyz.com:26656, ...)")
	cmd.Flags().StringVar(&b.startingIPAddress, "starting-ip-address", "",
		"starting IP address ("+
			"\"192.168.0.1\""+
			" results in persistent peers list ID0@192.168.0.1:26656, ID1@192.168.0.2:26656, ...)")
	cmd.Flags().StringArrayVar(&b.hostnames, "hostname", []string{},
		"manually override all hostnames of validators and non-validators (use --hostname multiple times for multiple hosts)")
	cmd.Flags().IntVar(&b.p2pPort, "p2p-port", 26656,
		"P2P Port")
	cmd.Flags().BoolVar(&b.randomMonikers, "random-monikers", false,
		"randomize the moniker for each generated node")

	return &cmd
}

func (b *builderTestnet) testnetFiles(cmd *cobra.Command, args []string) error {
	if len(b.hostnames) > 0 && len(b.hostnames) != (b.nValidators+b.nNonValidators) {
		return fmt.Errorf(
			"testnet needs precisely %d hostnames (number of validators plus non-validators) if --hostname parameter is used",
			b.nValidators+b.nNonValidators,
		)
	}

	config := cfg.DefaultConfig()

	// overwrite default config if set and valid
	if b.configFile != "" {
		viper.SetConfigFile(b.configFile)
		if err := viper.ReadInConfig(); err != nil {
			return err
		}
		if err := viper.Unmarshal(config); err != nil {
			return err
		}
		if err := config.ValidateBasic(); err != nil {
			return err
		}
	}

	genVals := make([]types.GenesisValidator, b.nValidators)

	for i := 0; i < b.nValidators; i++ {
		nodeDirName := fmt.Sprintf("%s%d", b.nodeDirPrefix, i)
		nodeDir := filepath.Join(b.outputDir, nodeDirName)
		config.SetRoot(nodeDir)

		err := os.MkdirAll(filepath.Join(nodeDir, "config"), nodeDirPerm)
		if err != nil {
			_ = os.RemoveAll(b.outputDir)
			return err
		}
		err = os.MkdirAll(filepath.Join(nodeDir, "data"), nodeDirPerm)
		if err != nil {
			_ = os.RemoveAll(b.outputDir)
			return err
		}

		if err := initFilesWithConfig(config, b.root.logger); err != nil {
			return err
		}

		pvKeyFile := filepath.Join(nodeDir, config.BaseConfig.PrivValidatorKey)
		pvStateFile := filepath.Join(nodeDir, config.BaseConfig.PrivValidatorState)
		pv := privval.LoadFilePV(pvKeyFile, pvStateFile)

		pubKey, err := pv.GetPubKey()
		if err != nil {
			return fmt.Errorf("can't get pubkey: %w", err)
		}
		genVals[i] = types.GenesisValidator{
			Address: pubKey.Address(),
			PubKey:  pubKey,
			Power:   1,
			Name:    nodeDirName,
		}
	}

	for i := 0; i < b.nNonValidators; i++ {
		nodeDir := filepath.Join(b.outputDir, fmt.Sprintf("%s%d", b.nodeDirPrefix, i+b.nValidators))
		config.SetRoot(nodeDir)

		err := os.MkdirAll(filepath.Join(nodeDir, "config"), nodeDirPerm)
		if err != nil {
			_ = os.RemoveAll(b.outputDir)
			return err
		}

		err = os.MkdirAll(filepath.Join(nodeDir, "data"), nodeDirPerm)
		if err != nil {
			_ = os.RemoveAll(b.outputDir)
			return err
		}

		if err := initFilesWithConfig(config, b.root.logger); err != nil {
			return err
		}
	}

	// Generate genesis doc from generated validators
	genDoc := &types.GenesisDoc{
		ChainID:         "chain-" + tmrand.Str(6),
		ConsensusParams: types.DefaultConsensusParams(),
		GenesisTime:     tmtime.Now(),
		InitialHeight:   b.initialHeight,
		Validators:      genVals,
	}

	// Write genesis file.
	for i := 0; i < b.nValidators+b.nNonValidators; i++ {
		nodeDir := filepath.Join(b.outputDir, fmt.Sprintf("%s%d", b.nodeDirPrefix, i))
		if err := genDoc.SaveAs(filepath.Join(nodeDir, config.BaseConfig.Genesis)); err != nil {
			_ = os.RemoveAll(b.outputDir)
			return err
		}
	}

	// Gather persistent peer addresses.
	var (
		persistentPeers string
		err             error
	)
	if b.populatePersistentPeers {
		persistentPeers, err = b.persistentPeersString(config)
		if err != nil {
			_ = os.RemoveAll(b.outputDir)
			return err
		}
	}

	// Overwrite default config.
	for i := 0; i < b.nValidators+b.nNonValidators; i++ {
		nodeDir := filepath.Join(b.outputDir, fmt.Sprintf("%s%d", b.nodeDirPrefix, i))
		config.SetRoot(nodeDir)
		config.P2P.AddrBookStrict = false
		config.P2P.AllowDuplicateIP = true
		if b.populatePersistentPeers {
			config.P2P.PersistentPeers = persistentPeers
		}
		config.Moniker = b.moniker(i)

		cfg.WriteConfigFile(filepath.Join(nodeDir, "config", "config.toml"), config)
	}

	fmt.Printf("Successfully initialized %v node directories\n", b.nValidators+b.nNonValidators)
	return nil
}

func (b *builderTestnet) hostnameOrIP(i int) string {
	if len(b.hostnames) > 0 && i < len(b.hostnames) {
		return b.hostnames[i]
	}
	if b.startingIPAddress == "" {
		return fmt.Sprintf("%s%d%s", b.hostnamePrefix, i, b.hostnameSuffix)
	}
	ip := net.ParseIP(b.startingIPAddress)
	ip = ip.To4()
	if ip == nil {
		fmt.Printf("%v: non ipv4 address\n", b.startingIPAddress)
		os.Exit(1)
	}

	for j := 0; j < i; j++ {
		ip[3]++
	}
	return ip.String()
}

func (b *builderTestnet) persistentPeersString(config *cfg.Config) (string, error) {
	persistentPeers := make([]string, b.nValidators+b.nNonValidators)
	for i := 0; i < b.nValidators+b.nNonValidators; i++ {
		nodeDir := filepath.Join(b.outputDir, fmt.Sprintf("%s%d", b.nodeDirPrefix, i))
		config.SetRoot(nodeDir)
		nodeKey, err := p2p.LoadNodeKey(config.NodeKeyFile())
		if err != nil {
			return "", err
		}
		persistentPeers[i] = p2p.IDAddressString(nodeKey.ID(), fmt.Sprintf("%s:%d", b.hostnameOrIP(i), b.p2pPort))
	}
	return strings.Join(persistentPeers, ","), nil
}

func (b *builderTestnet) moniker(i int) string {
	if b.randomMonikers {
		return randomMoniker()
	}
	if len(b.hostnames) > 0 && i < len(b.hostnames) {
		return b.hostnames[i]
	}
	if b.startingIPAddress == "" {
		return fmt.Sprintf("%s%d%s", b.hostnamePrefix, i, b.hostnameSuffix)
	}
	return randomMoniker()
}

func randomMoniker() string {
	return bytes.HexBytes(tmrand.Bytes(8)).String()
}
