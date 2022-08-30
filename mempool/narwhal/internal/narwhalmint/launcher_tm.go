package narwhalmint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tendermint/tendermint/config"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	tmhttp "github.com/tendermint/tendermint/rpc/client/http"
	jsonrpcclient "github.com/tendermint/tendermint/rpc/jsonrpc/client"
	"github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"
)

const (
	dirPerm         = 0755
	tmNodeDirPrefix = "node"
	localhost       = "localhost"
)

type TMClient struct {
	NodeName string
	*tmhttp.HTTP
}

type LauncherTendermint struct {
	Host         string
	Out          io.Writer
	ProxyAppType string

	mNodeCFGs        map[string]*config.Config
	clients          []*TMClient
	dirs             testDirs
	portFactory      *portFactory
	runtimeErrStream <-chan error
}

func (l *LauncherTendermint) Dir() string {
	return l.dirs.rootDir
}

func (l *LauncherTendermint) NodeRuntimeErrs() <-chan error {
	return l.runtimeErrStream
}

func (l *LauncherTendermint) Clients() []*TMClient {
	return l.clients
}

func (l *LauncherTendermint) SetupFS(now time.Time, narwhalCFGs []*config.NarwhalMempoolConfig) error {
	if l.portFactory == nil {
		l.portFactory = new(portFactory)
		defer l.portFactory.close()
	}
	l.setDefaults()

	l.dirs = newTestResultsDir(now, "tendermint")

	cfg := config.DefaultConfig()
	err := config.UnmarshalConfig([]byte(configTOML), cfg)
	if err != nil {
		return err
	}

	nodeNames, err := l.setupTMFS(cfg, now, narwhalCFGs)
	if err != nil {
		return err
	}
	l.dirs.nodeNames = nodeNames

	return nil
}

func (l *LauncherTendermint) StartFrom(ctx context.Context, rootDir string) error {
	if tm := "tendermint"; filepath.Base(rootDir) != tm {
		rootDir = filepath.Join(rootDir, tm)
	}
	l.setDefaults()

	l.dirs = testDirs{rootDir: rootDir}

	nodeDirs, err := os.ReadDir(l.dirs.nodesDir())
	if err != nil {
		return err
	}

	var (
		nodeNames []string
		mCFGs     = make(map[string]*config.Config)
	)
	for _, nodeDir := range nodeDirs {
		nodeName := nodeDir.Name()
		nodeNames = append(nodeNames, nodeName)
		cfg, err := config.ReadConfigFile(l.dirs.configFile(nodeName))
		if err != nil {
			return err
		}
		mCFGs[nodeName] = cfg
	}
	l.dirs.nodeNames = nodeNames
	l.mNodeCFGs = mCFGs

	return l.Start(ctx)
}

func (l *LauncherTendermint) Start(ctx context.Context) error {
	errStream, err := l.runAllNodes(ctx)
	if err != nil {
		return err
	}
	l.runtimeErrStream = errStream

	var clients []*TMClient
	for nodeName, cfg := range l.mNodeCFGs {
		tmHTTP, err := newTMHTTP(cfg.RPC.ListenAddress)
		if err != nil {
			return err
		}
		clients = append(clients, &TMClient{
			NodeName: nodeName,
			HTTP:     tmHTTP,
		})
	}
	l.clients = clients

	return nil
}

func (l *LauncherTendermint) runAllNodes(ctx context.Context) (<-chan error, error) {
	errStream := make(chan error)
	readyMsgStream := make(chan readyMsg)
	go func() {
		defer close(errStream)

		wg := new(sync.WaitGroup)
		for i := range l.dirs.nodeNames {
			wg.Add(1)
			go func(nodeName string) {
				defer wg.Done()

				err := l.runTMValidator(ctx, readyMsgStream, nodeName)
				if err != nil && err != context.Canceled && !isSignalKilledErr(err) {
					errStream <- err
				}
			}(l.dirs.nodeNames[i])
		}
		wg.Wait()
	}()

	select {
	case <-ctx.Done():
	case err := <-l.awaitStartup(ctx, readyMsgStream):
		return errStream, err
	}

	return errStream, nil
}

func (l *LauncherTendermint) awaitStartup(ctx context.Context, readyMsgStream <-chan readyMsg) <-chan error {
	mNodes := make(map[string]struct{})
	for _, nodeName := range l.dirs.nodeNames {
		mNodes[nodeName] = struct{}{}
	}
	return awaitFn(ctx, readyMsgStream, func(msg readyMsg) (bool, error) {
		nName := msg.nodeName
		delete(mNodes, nName)

		var err error
		if msg.status != "ready" {
			logFile := l.dirs.nodeLogFile(nName, nName)
			err = fmt.Errorf("failed to start tm node(%s): see %s for logs", nName, logFile)
		}
		return len(mNodes) == 0, err
	})
}

func (l *LauncherTendermint) runTMValidator(ctx context.Context, readyStream chan<- readyMsg, nodeName string) error {
	f, closeFn, err := newLogFileWriter(l.dirs.nodeLogFile(nodeName, nodeName))
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer closeFn()

	args := []string{
		"node",
		"--home", l.dirs.nodeDir(nodeName),
		"--proxy_app", l.ProxyAppType,
	}
	cmd := exec.CommandContext(ctx, "tendermint", args...)
	readyWriter := newReadyTailWriter(ctx, readyStream, "validator", nodeName, "", tmLineCheckFn)
	mf := io.MultiWriter(f, readyWriter)
	cmd.Stdout, cmd.Stderr = mf, mf

	return cmd.Run()
}

type tmNode struct {
	id       int
	name     string
	rpc, p2p string
}

func (l *LauncherTendermint) setupTMFS(cfg *config.Config, now time.Time, narwhalCFGs []*config.NarwhalMempoolConfig) (nodes []string, e error) {
	var (
		genVals = make([]types.GenesisValidator, len(narwhalCFGs))
		mNodes  = make([]tmNode, len(narwhalCFGs))
	)
	for i := range narwhalCFGs {
		nodeName := fmt.Sprintf("%s%d", tmNodeDirPrefix, i)
		nodeDir := l.dirs.nodeDir(nodeName)
		cfg.SetRoot(nodeDir)

		randoPorts, err := l.portFactory.newRandomPorts(3, l.Host)
		if err != nil {
			return nil, err
		}
		mNodes[i] = tmNode{
			id:   i,
			name: nodeName,
			rpc:  randoPorts[0],
			p2p:  randoPorts[1],
		}

		err = createDirs(
			l.dirs.configDir(nodeName),
			l.dirs.dataDir(nodeName),
			l.dirs.nodeLogDir(nodeName),
		)
		if err != nil {
			return nil, err
		}

		err = initFilesWithConfig(cfg)
		if err != nil {
			return nil, err
		}

		pvKeyFile := filepath.Join(nodeDir, cfg.BaseConfig.PrivValidatorKey)
		pvStateFile := filepath.Join(nodeDir, cfg.BaseConfig.PrivValidatorState)
		pv := privval.LoadFilePV(pvKeyFile, pvStateFile)

		pubKey, err := pv.GetPubKey()
		if err != nil {
			return nil, fmt.Errorf("can't get pubkey: %w", err)
		}
		genVals[i] = types.GenesisValidator{
			Address: pubKey.Address(),
			PubKey:  pubKey,
			Power:   1,
			Name:    nodeName,
		}
	}

	// Generate genesis doc from generated validators
	genDoc := &types.GenesisDoc{
		ChainID:         "chain-" + tmrand.Str(6),
		ConsensusParams: types.DefaultConsensusParams(),
		GenesisTime:     now,
		Validators:      genVals,
	}

	mPersistentPeers, err := persistentPeersString(cfg, l.dirs, l.Host, mNodes)
	if err != nil {
		return nil, err
	}

	persistentPeerFn := func(nodeName string) string {
		var out []string
		for name, peerStr := range mPersistentPeers {
			if name == nodeName {
				continue
			}
			out = append(out, peerStr)
		}
		return strings.Join(out, ",")
	}

	newListenAddr := func(port string) string {
		return "tcp://" + net.JoinHostPort(l.Host, port)
	}

	var (
		mNodeCFGs = make(map[string]*config.Config)
		nodeNames []string
	)
	// Overwrite default cfg.
	for _, node := range mNodes {
		narwhalCFG := narwhalCFGs[node.id]
		cfg.SetRoot(l.dirs.nodeDir(node.name))

		if err := genDoc.SaveAs(cfg.GenesisFile()); err != nil {
			return nil, fmt.Errorf("failed to save genesis doc: %w", err)
		}
		cfg.Consensus.ConsensusPartSetFnOverride = "sans_data"
		cfg.Narwhal = narwhalCFG
		cfg.RPC.ListenAddress = newListenAddr(node.rpc)
		cfg.P2P.ListenAddress = newListenAddr(node.p2p)
		cfg.P2P.AddrBookStrict = false
		cfg.P2P.AllowDuplicateIP = true
		cfg.P2P.PersistentPeers = persistentPeerFn(node.name)
		cfg.Moniker = moniker()
		config.WriteConfigFile(l.dirs.configFile(node.name), cfg)
		nodeNames = append(nodeNames, node.name)
		mNodeCFGs[node.name] = copyCFG(cfg)
	}
	l.mNodeCFGs = mNodeCFGs

	return nodeNames, nil
}

func (l *LauncherTendermint) setDefaults() {
	if l.Host == "" {
		l.Host = localhost
	}
	if l.ProxyAppType == "" {
		l.ProxyAppType = "noop"
	}
}

func initFilesWithConfig(cfg *config.Config) error {
	// private validator
	privValKeyFile := cfg.PrivValidatorKeyFile()
	privValStateFile := cfg.PrivValidatorStateFile()
	pv := privval.GenFilePV(privValKeyFile, privValStateFile)
	pv.Save()

	nodeKeyFile := cfg.NodeKeyFile()
	if _, err := p2p.LoadOrGenNodeKey(nodeKeyFile); err != nil {
		return fmt.Errorf("faield to create keys: %w", err)
	}

	// genesis file
	genDoc := types.GenesisDoc{
		ChainID:         fmt.Sprintf("test-chain-%v", tmrand.Str(6)),
		GenesisTime:     tmtime.Now(),
		ConsensusParams: types.DefaultConsensusParams(),
	}
	pubKey, err := pv.GetPubKey()
	if err != nil {
		return fmt.Errorf("can't get pubkey: %w", err)
	}
	genDoc.Validators = []types.GenesisValidator{{
		Address: pubKey.Address(),
		PubKey:  pubKey,
		Power:   10,
	}}

	err = genDoc.SaveAs(cfg.GenesisFile())
	if err != nil {
		return fmt.Errorf("failed to save gen doc genesis file: %w", err)
	}
	return nil
}

func persistentPeersString(cfg *config.Config, dirs testDirs, host string, nodes []tmNode) (map[string]string, error) {
	persistentPeers := make(map[string]string, len(nodes))
	for _, node := range nodes {
		nodeDir := dirs.nodeDir(node.name)
		cfg.SetRoot(nodeDir)
		nodeKey, err := p2p.LoadNodeKey(cfg.NodeKeyFile())
		if err != nil {
			return nil, err
		}
		hostPort := net.JoinHostPort(host, node.p2p)
		persistentPeers[node.name] = p2p.IDAddressString(nodeKey.ID(), hostPort)
	}
	return persistentPeers, nil
}

func moniker() string {
	return tmbytes.HexBytes(tmrand.Bytes(8)).String()
}

var (
	tmValidatorReadyMsg      = []byte("Ensure peers")
	tmValidatorFailedMsg     = []byte("ERROR: failed to create node")
	tmValidatorFailedGoLvlDB = []byte("dial tcp: address goleveldb: missing port in address")
)

func tmLineCheckFn(b []byte, nodeType string) string {
	isFailed := bytes.Contains(b, tmValidatorFailedMsg) || bytes.Contains(b, tmValidatorFailedGoLvlDB)
	isReady := bytes.Contains(b, tmValidatorReadyMsg)

	var status string
	switch {
	case isFailed:
		status = "failed start"
	case isReady:
		status = "ready"
	}

	return status
}

func copyCFG(cfg *config.Config) *config.Config {
	cfgCopy := config.Config{
		BaseConfig: cfg.BaseConfig,
	}
	if cfg.RPC != nil {
		rpc := *cfg.RPC
		cfgCopy.RPC = &rpc
	}
	if cfg.P2P != nil {
		peer := *cfg.P2P
		cfgCopy.P2P = &peer
	}
	if cfg.Mempool != nil {
		mem := *cfg.Mempool
		cfgCopy.Mempool = &mem
	}
	if cfg.Narwhal != nil {
		nar := *cfg.Narwhal
		cfgCopy.Narwhal = &nar
	}
	if cfg.StateSync != nil {
		ss := *cfg.StateSync
		cfgCopy.StateSync = &ss
	}
	if cfg.FastSync != nil {
		fs := *cfg.FastSync
		cfgCopy.FastSync = &fs
	}
	if cfg.Consensus != nil {
		cs := *cfg.Consensus
		cfgCopy.Consensus = &cs
	}
	if cfg.TxIndex != nil {
		index := *cfg.TxIndex
		cfgCopy.TxIndex = &index
	}
	if cfg.Instrumentation != nil {
		instr := *cfg.Instrumentation
		cfgCopy.Instrumentation = &instr
	}

	return &cfgCopy
}

func newTMHTTP(remote string) (*tmhttp.HTTP, error) {
	httpc, err := jsonrpcclient.DefaultHTTPClient(remote)
	if err != nil {
		return nil, err
	}
	httpc.Timeout = 5 * time.Second
	transDef := http.DefaultTransport.(*http.Transport).Clone()
	transDef.DialContext = httpc.Transport.(*http.Transport).DialContext
	httpc.Transport = transDef

	return tmhttp.NewWithClient(remote, "/websockets", httpc)
}

const (
	configTOML = `
[mempool]
recheck = false
broadcast = false
size = 0

[consensus]
create_empty_blocks = false
`
)
