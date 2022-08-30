package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	dbm "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	tmmath "github.com/tendermint/tendermint/libs/math"
	tmos "github.com/tendermint/tendermint/libs/os"
	"github.com/tendermint/tendermint/light"
	lproxy "github.com/tendermint/tendermint/light/proxy"
	lrpc "github.com/tendermint/tendermint/light/rpc"
	dbs "github.com/tendermint/tendermint/light/store/db"
	rpcserver "github.com/tendermint/tendermint/rpc/jsonrpc/server"
)

var (
	primaryKey   = []byte("primary")
	witnessesKey = []byte("witnesses")
)

// lightCmd represents the base command when called without any subcommands
func (b *builderRoot) lightCmd() *cobra.Command {
	lb := lightBuilder{cfg: b.cfg}
	return lb.cmd()
}

type lightBuilder struct {
	cfg *config.Config

	listenAddr         string
	primaryAddr        string
	witnessAddrsJoined string
	home               string
	maxOpenConnections int

	sequential     bool
	trustingPeriod time.Duration
	trustedHeight  int64
	trustedHash    []byte
	trustLevelStr  string

	verbose bool
}

func (lb *lightBuilder) cmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "light [chainID]",
		Short: "Run a light client proxy server, verifying Tendermint rpc",
		Long: `Run a light client proxy server, verifying Tendermint rpc.

All calls that can be tracked back to a block header by a proof
will be verified before passing them back to the caller. Other than
that, it will present the same interface as a full Tendermint node.

Furthermore to the chainID, a fresh instance of a light client will
need a primary RPC address, a trusted hash and height and witness RPC addresses
(if not using sequential verification). To restart the node, thereafter
only the chainID is required.

When /abci_query is called, the Merkle key path format is:

	/{store name}/{key}

Please verify with your application that this Merkle key format is used (true
for applications built w/ Cosmos SDK).
`,
		RunE: lb.runProxy,
		Args: cobra.ExactArgs(1),
		Example: `light cosmoshub-3 -p http://52.57.29.196:26657 -w http://public-seed-node.cosmoshub.certus.one:26657
	--height 962118 --hash 28B97BE9F6DE51AC69F70E0B7BFD7E5C9CD1A595B7DC31AFF27C50D4948020CD`,
	}

	cmd.Flags().StringVar(&lb.listenAddr, "laddr", "tcp://localhost:8888",
		"serve the proxy on the given address")
	cmd.Flags().StringVarP(&lb.primaryAddr, "primary", "p", "",
		"connect to a Tendermint node at this address")
	cmd.Flags().StringVarP(&lb.witnessAddrsJoined, "witnesses", "w", "",
		"tendermint nodes to cross-check the primary node, comma-separated")
	cmd.Flags().StringVar(&lb.home, "home-dir", os.ExpandEnv(filepath.Join("$HOME", ".tendermint-light")),
		"specify the home directory")
	cmd.Flags().IntVar(
		&lb.maxOpenConnections,
		"max-open-connections",
		900,
		"maximum number of simultaneous connections (including WebSocket).")
	cmd.Flags().DurationVar(&lb.trustingPeriod, "trusting-period", 168*time.Hour,
		"trusting period that headers can be verified within. Should be significantly less than the unbonding period")
	cmd.Flags().Int64Var(&lb.trustedHeight, "height", 1, "Trusted header's height")
	cmd.Flags().BytesHexVar(&lb.trustedHash, "hash", []byte{}, "Trusted header's hash")
	cmd.Flags().BoolVar(&lb.verbose, "verbose", false, "Verbose output")
	cmd.Flags().StringVar(&lb.trustLevelStr, "trust-level", "1/3",
		"trust level. Must be between 1/3 and 3/3",
	)
	cmd.Flags().BoolVar(&lb.sequential, "sequential", false,
		"sequential verification. Verify all headers sequentially as opposed to using skipping verification",
	)

	return &cmd
}

func (lb *lightBuilder) runProxy(cmd *cobra.Command, args []string) error {
	// Initialise logger.
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	var option log.Option
	if lb.verbose {
		option, _ = log.AllowLevel("debug")
	} else {
		option, _ = log.AllowLevel("info")
	}
	logger = log.NewFilter(logger, option)

	chainID := args[0]
	logger.Info("Creating client...", "chainID", chainID)

	witnessesAddrs := []string{}
	if lb.witnessAddrsJoined != "" {
		witnessesAddrs = strings.Split(lb.witnessAddrsJoined, ",")
	}

	db, err := dbm.NewGoLevelDB("light-client-db", lb.home)
	if err != nil {
		return fmt.Errorf("can't create a db: %w", err)
	}

	if lb.primaryAddr == "" { // check to see if we can start from an existing state
		var err error
		lb.primaryAddr, witnessesAddrs, err = checkForExistingProviders(db)
		if err != nil {
			return fmt.Errorf("failed to retrieve primary or witness from db: %w", err)
		}
		if lb.primaryAddr == "" {
			return errors.New("no primary address was provided nor found. Please provide a primary (using -p)." +
				" Run the command: tendermint light --help for more information")
		}
	} else {
		err := saveProviders(db, lb.primaryAddr, lb.witnessAddrsJoined)
		if err != nil {
			logger.Error("Unable to save primary and or witness addresses", "err", err)
		}
	}

	trustLevel, err := tmmath.ParseFraction(lb.trustLevelStr)
	if err != nil {
		return fmt.Errorf("can't parse trust level: %w", err)
	}

	options := []light.Option{
		light.Logger(logger),
		light.ConfirmationFunction(func(action string) bool {
			fmt.Println(action)
			scanner := bufio.NewScanner(os.Stdin)
			for {
				scanner.Scan()
				response := scanner.Text()
				switch response {
				case "y", "Y":
					return true
				case "n", "N":
					return false
				default:
					fmt.Println("please input 'Y' or 'n' and press ENTER")
				}
			}
		}),
	}

	if lb.sequential {
		options = append(options, light.SequentialVerification())
	} else {
		options = append(options, light.SkippingVerification(trustLevel))
	}

	var c *light.Client
	if lb.trustedHeight > 0 && len(lb.trustedHash) > 0 { // fresh installation
		c, err = light.NewHTTPClient(
			context.Background(),
			chainID,
			light.TrustOptions{
				Period: lb.trustingPeriod,
				Height: lb.trustedHeight,
				Hash:   lb.trustedHash,
			},
			lb.primaryAddr,
			witnessesAddrs,
			dbs.New(db, chainID),
			options...,
		)
	} else { // continue from latest state
		c, err = light.NewHTTPClientFromTrustedStore(
			chainID,
			lb.trustingPeriod,
			lb.primaryAddr,
			witnessesAddrs,
			dbs.New(db, chainID),
			options...,
		)
	}
	if err != nil {
		return err
	}

	cfg := rpcserver.DefaultConfig()
	cfg.MaxBodyBytes = lb.cfg.RPC.MaxBodyBytes
	cfg.MaxHeaderBytes = lb.cfg.RPC.MaxHeaderBytes
	cfg.MaxOpenConnections = lb.maxOpenConnections
	// If necessary adjust global WriteTimeout to ensure it's greater than
	// TimeoutBroadcastTxCommit.
	// See https://github.com/tendermint/tendermint/issues/3435
	if cfg.WriteTimeout <= lb.cfg.RPC.TimeoutBroadcastTxCommit {
		cfg.WriteTimeout = lb.cfg.RPC.TimeoutBroadcastTxCommit + 1*time.Second
	}

	p, err := lproxy.NewProxy(c, lb.listenAddr, lb.primaryAddr, cfg, logger, lrpc.KeyPathFn(lrpc.DefaultMerkleKeyPathFn()))
	if err != nil {
		return err
	}

	// Stop upon receiving SIGTERM or CTRL-C.
	tmos.TrapSignal(logger, func() {
		p.Listener.Close()
	})

	logger.Info("Starting proxy...", "laddr", lb.listenAddr)
	if err := p.ListenAndServe(); err != http.ErrServerClosed {
		// Error starting or closing listener:
		logger.Error("proxy ListenAndServe", "err", err)
	}

	return nil
}

func checkForExistingProviders(db dbm.DB) (string, []string, error) {
	primaryBytes, err := db.Get(primaryKey)
	if err != nil {
		return "", []string{""}, err
	}
	witnessesBytes, err := db.Get(witnessesKey)
	if err != nil {
		return "", []string{""}, err
	}
	witnessesAddrs := strings.Split(string(witnessesBytes), ",")
	return string(primaryBytes), witnessesAddrs, nil
}

func saveProviders(db dbm.DB, primaryAddr, witnessesAddrs string) error {
	err := db.Set(primaryKey, []byte(primaryAddr))
	if err != nil {
		return fmt.Errorf("failed to save primary provider: %w", err)
	}
	err = db.Set(witnessesKey, []byte(witnessesAddrs))
	if err != nil {
		return fmt.Errorf("failed to save witness providers: %w", err)
	}
	return nil
}
