package narwhalmint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tendermint/tendermint/config"
)

// LauncherNarwhal is can set up and run a narwhal cluster.
type LauncherNarwhal struct {
	BatchSize   int           // will default to 5000
	BatchDelay  time.Duration // defaults to 200ms
	HeaderSize  int           // will default to 500
	HeaderDelay time.Duration // defaults to 500ms
	Host        string
	Out         io.Writer
	OutputDir   string
	Primaries   int // will default to 4 when not set
	Workers     int // will default to 1 when not set

	dirs                testDirs
	committeeCFG        committeePrimariesCFG
	mWorkerNetworkKeys  map[string]map[int]string
	mPrimaryNetworkKeys map[string]string
	runtimeErrStream    <-chan error
	status              string
}

func (l *LauncherNarwhal) TMOpts(p2pPort, rpcPort string) []TMOpts {
	out := make([]TMOpts, 0, len(l.committeeCFG.Authorities))
	for pk, primary := range l.committeeCFG.Authorities {
		cfg := config.NarwhalMempoolConfig{
			PrimaryAddr:             primary.GRPC.HostPort(),
			PrimaryEncodedPublicKey: pk,
		}

		for workerID, worker := range primary.Workers {
			cfg.Workers = append(cfg.Workers, config.NarwhalWorkerConfig{
				Name: workerID,
				Addr: worker.Transactions.HostPort(),
			})
		}

		out = append(out, TMOpts{
			Host:       primary.PrimaryAddress.Host(),
			P2PPort:    p2pPort,
			RPCPort:    rpcPort,
			NarwhalCFG: &cfg,
		})
	}
	return out
}

// Dir is the root directory for the narwhal nodes to execute from.
func (l *LauncherNarwhal) Dir() string {
	return l.dirs.rootDir
}

type (
	NarwhalOpt struct {
		NodeName   string
		PrimHost   string
		PrimPort   string
		GRPCHost   string
		GRPCPort   string
		WorkerOpts []NarwhalWorkerOpt
	}

	NarwhalWorkerOpt struct {
		TxsHost    string
		TxsPort    string
		WorkerHost string
		WorkerPort string
	}
)

func (l *LauncherNarwhal) RenameDirs(opts ...NarwhalOpt) error {
	comm, err := readCommitteeFiles(l.dirs)
	if err != nil {
		return err
	}

	renamedToo := make(map[string]string)
	for _, opt := range opts {
		if opt.NodeName == "" {
			continue
		}
		renamedToo[opt.PrimHost] = opt.NodeName
	}

	for nodeName, n := range comm.Authorities {
		newName, ok := renamedToo[n.PrimaryAddress.Host()]
		if !ok {
			continue
		}
		err := os.Rename(l.dirs.nodeDir(nodeName), l.dirs.nodeDir(newName))
		if err != nil {
			return err
		}
	}

	return nil
}

// SetupFS sets up the filesystem to run the narwhal nodes.
func (l *LauncherNarwhal) SetupFS(ctx context.Context, now time.Time, opts ...NarwhalOpt) error {
	if l.BatchSize == 0 {
		l.BatchSize = 5000
	}
	if l.BatchDelay == 0 {
		l.BatchDelay = 200 * time.Millisecond
	}
	if l.HeaderSize == 0 {
		l.HeaderSize = 500
	}
	if l.HeaderDelay == 0 {
		l.HeaderDelay = 500 * time.Millisecond
	}
	if l.Primaries == 0 && len(opts) == 0 {
		l.Primaries = 4
		l.println("setting primaries to default value of 4")
	}
	if l.Workers == 0 {
		l.Workers = 1
		l.println("setting workers to default value of 1")
	}

	err := l.setupTestEnv(ctx, now, opts)
	if err != nil {
		return err
	}
	l.status = "setup"

	return nil
}

func (l *LauncherNarwhal) StartFrom(ctx context.Context, rootDir string) error {
	l.status = "setup"
	if narwhal := "narwhal"; filepath.Base(rootDir) != narwhal {
		rootDir = filepath.Join(rootDir, narwhal)
	}

	l.dirs.rootDir = rootDir

	nodeDirs, err := os.ReadDir(l.dirs.nodesDir())
	if err != nil {
		return err
	}

	l.committeeCFG, err = readCommitteeFiles(l.dirs)
	if err != nil {
		return err
	}

	var nodeNames []string
	for _, nodeDir := range nodeDirs {
		nodeName, err := readNarwhalKeyFile(l.dirs.nodeKeyFile(nodeDir.Name()))
		if err != nil {
			return err
		}
		nodeNames = append(nodeNames, nodeName)
	}
	l.dirs.nodeNames = nodeNames

	return l.Start(ctx)
}

// Start will start all the narwhal nodes. This will start up the nodes in separate
// go routines. The context can be provided to control stopping the running cluster.
// Additionally, calling Stop will also stop the cluster. When Start returns the nodes
// are in fully operational.
func (l *LauncherNarwhal) Start(ctx context.Context) error {
	if l.status == "" {
		return fmt.Errorf("the filesystem is not setup to run nodes; make sure to call SetupFS before running the nodes")
	}
	if l.status == "running" {
		return fmt.Errorf("the narwhal nodes are already running")
	}

	l.status = "running"
	runtimeErrStream, startupErr := l.runAllNodes(ctx)
	if startupErr != nil {
		l.status = "failed startup"
		return fmt.Errorf("failed to startup nodes: %w", startupErr)
	}
	l.runtimeErrStream = runtimeErrStream

	return nil
}

// NodeRuntimeErrs provides runtime errors encountered from running the narwhal nodes as
// a daemon process.
func (l *LauncherNarwhal) NodeRuntimeErrs() <-chan error {
	return l.runtimeErrStream
}

func (l *LauncherNarwhal) runAllNodes(ctx context.Context) (<-chan error, error) {
	errStream := make(chan error)

	readyMsgStream := make(chan readyMsg)
	defer close(readyMsgStream)

	go func(workers int) {
		defer close(errStream)

		wg := new(sync.WaitGroup)
		for i := range l.dirs.nodeNames {
			nName := l.dirs.nodeNames[i]
			workerCFGs := l.committeeCFG.Authorities[nName].Workers
			workerIDs := make([]string, 0, len(workerCFGs))
			for workerID := range workerCFGs {
				workerIDs = append(workerIDs, workerID)
			}

			// start primaries
			wg.Add(1)
			go func(nName string, workerIDs []string) {
				defer wg.Done()

				err := runPrimary(ctx, readyMsgStream, l.dirs, nName, workerIDs)
				if err != nil && err != context.Canceled && !isSignalKilledErr(err) {
					errStream <- err
				}
			}(nName, workerIDs)

			// start workers
			for workerID := range workerCFGs {
				wg.Add(1)
				go func(nName, workerID string, workerIDs []string) {
					defer wg.Done()

					err := runWorker(ctx, readyMsgStream, l.dirs, nName, workerIDs, workerID)
					if err != nil && err != context.Canceled && !isSignalKilledErr(err) {
						errStream <- err
					}
				}(nName, workerID, workerIDs)
			}
		}

		wg.Wait()
	}(l.Workers)

	select {
	case <-ctx.Done():
	case err := <-l.awaitStartup(ctx, readyMsgStream):
		return errStream, err
	}

	return errStream, nil
}

func (l *LauncherNarwhal) awaitStartup(ctx context.Context, readyMsgStream <-chan readyMsg) <-chan error {
	nodes := make(map[string]struct{})
	for nodeName, authCFG := range l.committeeCFG.Authorities {
		nodes[nodeName] = struct{}{}
		for workerID := range authCFG.Workers {
			nodes[nodeName+"_worker_"+workerID] = struct{}{}
		}
	}
	return awaitFn(ctx, readyMsgStream, func(msg readyMsg) (bool, error) {
		key := msg.nodeName
		if msg.nodeType == "worker" && msg.workerID != "" {
			key += "_worker_" + msg.workerID
		}
		delete(nodes, key)

		var err error
		if msg.status != "ready" {
			var workerDesc string
			if msg.nodeType == "worker" {
				workerDesc = fmt.Sprintf(" worker(%s)", msg.workerID)
			}

			label := msg.nodeType
			if label == "worker" {
				label = "worker_" + msg.workerID
			}
			logFile := l.dirs.nodeLogFile(msg.nodeName, label)
			err = fmt.Errorf("failed start for narwhal node(%s)%s: see %s for logs", msg.nodeName, workerDesc, logFile)
		}

		return len(nodes) == 0, err
	})
}

func (l *LauncherNarwhal) setupTestEnv(ctx context.Context, now time.Time, opts []NarwhalOpt) (e error) {
	if l.OutputDir != "" {
		l.dirs = testDirs{rootDir: filepath.Join(l.OutputDir, "narwhal")}
	} else {
		l.dirs = newTestResultsDir(now, "narwhal")
	}
	if err := createDirs(l.dirs.nodesDir()); err != nil {
		return err
	}

	numPrimaries := len(opts)
	if numPrimaries == 0 {
		numPrimaries = l.Primaries
	}

	nodeNames, mPrimNetworkKeys, mWorkerNetworkKeys, err := setupNodes(ctx, l.dirs, numPrimaries, l.Workers)
	if err != nil {
		return fmt.Errorf("failed to setup narwhal node directories: %w", err)
	}
	l.dirs.nodeNames = nodeNames
	l.mPrimaryNetworkKeys, l.mWorkerNetworkKeys = mPrimNetworkKeys, mWorkerNetworkKeys

	comCFG, err := l.setupCommitteeCFG(opts)
	if err != nil {
		return err
	}
	l.committeeCFG = comCFG

	err = writeCommitteeFiles(l.dirs, comCFG)
	if err != nil {
		return fmt.Errorf("failed to write committee file: %w", err)
	}

	err = l.writeParameterFiles()
	if err != nil {
		return fmt.Errorf("failed to write paramters file: %w", err)
	}

	return nil
}

func (l *LauncherNarwhal) writeParameterFiles() error {
	for nodeName, auth := range l.committeeCFG.Authorities {
		paramFile := l.dirs.nodeParameterFile(nodeName)
		err := writeParametersFile(paramFile, string(auth.GRPC), l.BatchSize, l.HeaderSize, l.BatchDelay, l.HeaderDelay)
		if err != nil {
			return fmt.Errorf("failed to write parameters file(%s): %w", paramFile, err)
		}
	}
	return nil
}

func (l *LauncherNarwhal) println(format string, args ...interface{}) {
	if l.Out == nil {
		return
	}
	fmt.Fprintln(l.Out, append([]interface{}{format}, args...)...)
}

func runPrimary(ctx context.Context, readyStream chan<- readyMsg, dirs testDirs, nodeName string, workerIDs []string) error {
	label := "primary"

	readyTailer := newNarwhalReadyWriter(ctx, readyStream, "primary", nodeName, "")
	err := runNodeExecCmd(ctx, readyTailer, dirs, nodeName, label, workerIDs, "primary", "--consensus-disabled")
	if err != nil {
		return fmt.Errorf("node %s encountered runtime error: %w", nodeName, err)
	}
	return nil
}

func runWorker(ctx context.Context, readyStream chan<- readyMsg, dirs testDirs, nodeName string, workerIDs []string, workerID string) error {
	label := "worker_" + workerID

	readyTailer := newNarwhalReadyWriter(ctx, readyStream, "worker", nodeName, workerID)
	err := runNodeExecCmd(ctx, readyTailer, dirs, nodeName, label, workerIDs, "worker", "--id", workerID)
	if err != nil {
		return fmt.Errorf("node %s worker %s encountered runtime error: %w", nodeName, workerID, err)
	}
	return nil
}

func runNodeExecCmd(ctx context.Context, readyTailer io.Writer, dirs testDirs, nodeName, label string, workerIDs []string, subCmd string, subCmdArgs ...string) error {
	f, closeFn, err := newLogFileWriter(dirs.nodeLogFile(nodeName, label))
	if err != nil {
		return fmt.Errorf("failed to create log file for node %s: %w", nodeName, err)
	}
	defer closeFn()

	// the argument/flag intertwining is required by the node cli
	args := []string{
		"-vvv", // set logs to debug
		"run",  // first subcommand
		"--primary-keys", dirs.nodeKeyFile(nodeName),
		"--committee", dirs.committeePrimaryFile(),
		"--primary-network-keys", dirs.nodeNetworkPrimaryKeyFile(nodeName),
		"--parameters", dirs.nodeParameterFile(nodeName),
		"--store", dirs.nodeDBDir(nodeName, label),
		"--workers", dirs.committeeWorkerFile(),
		"--worker-keys", dirs.nodeNetworkWorkerKeyFiles(nodeName, workerIDs),
	}
	args = append(args, subCmd)
	args = append(args, subCmdArgs...)
	cmd := exec.CommandContext(ctx, "narwhal_node", args...)
	cmd.Stdout, cmd.Stderr = f, io.MultiWriter(readyTailer, f)

	return cmd.Run()
}

func setupNodes(ctx context.Context, testDir testDirs, numPrimaries, numWorkers int) ([]string, map[string]string, map[string]map[int]string, error) {
	nodeNames := make([]string, 0, numPrimaries)
	mPrimNetworkKeys := make(map[string]string)
	mWorkerNetworkKeys := make(map[string]map[int]string)
	for i := 0; i < numPrimaries; i++ {
		nodeName, networkKey, workerNetworkKeys, err := setupNodeDir(ctx, testDir, numWorkers)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to setup node dir: %w", err)
		}
		nodeNames = append(nodeNames, nodeName)
		mPrimNetworkKeys[nodeName] = networkKey
		mWorkerNetworkKeys[nodeName] = workerNetworkKeys
	}
	return nodeNames, mPrimNetworkKeys, mWorkerNetworkKeys, nil
}

func setupNodeDir(ctx context.Context, testDir testDirs, numWorkers int) (string, string, map[int]string, error) {
	nodeName, err := newKeyFile(ctx, testDir)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create new key file: %w", err)
	}

	networkKey, err := newNetworkKeyFile(ctx, testDir.nodeNetworkPrimaryKeyFile(nodeName))
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create primary network key file: %w", err)
	}

	mWorkerNetworkKeys := make(map[int]string, numWorkers)
	for i := 0; i < numWorkers; i++ {
		networkKey, err := newNetworkKeyFile(ctx, testDir.nodeNetworkWorkerKeyFile(nodeName, strconv.Itoa(i)))
		if err != nil {
			return "", "", nil, fmt.Errorf("failed to write worker %d network key file: %w", i, err)
		}
		mWorkerNetworkKeys[i] = networkKey
	}

	err = createDirs(testDir.nodeLogDir(nodeName))
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create log dir for node %s: %w", nodeName, err)
	}

	return nodeName, networkKey, mWorkerNetworkKeys, nil
}

func newKeyFile(ctx context.Context, testDir testDirs) (string, error) {
	tmpFile := filepath.Join(testDir.nodesDir(), strconv.Itoa(rand.Int()))
	cmd := exec.CommandContext(ctx, "narwhal_node", "generate_keys", "--filename", tmpFile)
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute generate_keys: %w", err)
	}

	var keyFile struct {
		Name string `json:"name"`
	}
	err = readJSONFile(tmpFile, &keyFile)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal key file: %w", err)
	}

	nodeDir := testDir.nodeDir(keyFile.Name)
	err = createDirs(nodeDir)
	if err != nil {
		return "", fmt.Errorf("failed to create %s dir: %w", nodeDir, err)
	}

	newKeyFilename := testDir.nodeKeyFile(keyFile.Name)
	err = os.Rename(tmpFile, newKeyFilename)
	if err != nil {
		return "", fmt.Errorf("failed to rename %s to %s: %w", tmpFile, newKeyFilename, err)
	}

	return keyFile.Name, nil
}

func newNetworkKeyFile(ctx context.Context, filename string) (string, error) {
	cmd := exec.CommandContext(ctx, "narwhal_node", "generate_network_keys", "--filename", filename)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	var keyFile struct {
		Name string `json:"name"`
	}
	err := readJSONFile(filename, &keyFile)
	if err != nil {
		return "", err
	}
	return keyFile.Name, nil
}

type (
	committeePrimariesCFG struct {
		Authorities map[string]AuthorityCFG `json:"authorities"`
		Epoch       int                     `json:"epoch"`
	}

	committeeWorkersCFG struct {
		Epoch   int                             `json:"epoch"`
		Workers map[string]map[string]WorkerCFG `json:"workers"`
	}

	AuthorityCFG struct {
		NetworkKey     string               `json:"network_key"`
		PrimaryAddress Multiaddr            `json:"primary_address"`
		GRPC           Multiaddr            `json:"-"`
		Stake          int                  `json:"stake"`
		Workers        map[string]WorkerCFG `json:"-"`
	}

	WorkerCFG struct {
		NetworkKey   string    `json:"name"`
		Transactions Multiaddr `json:"transactions"`
		WorkerAddr   Multiaddr `json:"worker_address"`
	}
)

type Multiaddr string

func (m Multiaddr) Host() string {
	parts := strings.Split(string(m), "/")
	if len(parts) < 6 {
		return ""
	}
	return parts[2]
}

func (m Multiaddr) HostPort() string {
	return net.JoinHostPort(m.Host(), m.Port())
}

func (m Multiaddr) Port() string {
	parts := strings.Split(string(m), "/")
	if len(parts) < 6 {
		return ""
	}
	return parts[4]
}

func writeCommitteeFiles(dirs testDirs, cfg committeePrimariesCFG) error {
	err := writeJSONFile(dirs.committeePrimaryFile(), cfg)
	if err != nil {
		return fmt.Errorf("failed to write primary comittee file: %w", err)
	}

	workerCFG := committeeWorkersCFG{
		Epoch:   cfg.Epoch,
		Workers: make(map[string]map[string]WorkerCFG, len(cfg.Authorities)),
	}
	for nodeName := range cfg.Authorities {
		workerCFG.Workers[nodeName] = cfg.Authorities[nodeName].Workers
	}

	err = writeJSONFile(dirs.committeeWorkerFile(), workerCFG)
	if err != nil {
		return fmt.Errorf("failed to write worker committee file: %w", err)
	}

	return nil
}

func writeJSONFile(filename string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, b, os.ModePerm)
}

func (l *LauncherNarwhal) setupCommitteeCFG(opts []NarwhalOpt) (committeePrimariesCFG, error) {
	portFact := new(portFactory)
	defer portFact.close()

	committee := committeePrimariesCFG{
		Authorities: make(map[string]AuthorityCFG, len(l.dirs.nodeNames)),
	}
	for i, authority := range l.dirs.nodeNames {
		var opt NarwhalOpt
		if i < len(opts) {
			opt = opts[i]
		}
		primAddr, grpcAddr, err := newPrimaryCFG(portFact, l.Host, opt)
		if err != nil {
			return committeePrimariesCFG{}, fmt.Errorf("failed to create pimary cfg: %w", err)
		}

		workerCFGs, err := newWorkerCFGs(portFact, l.Host, l.Workers, opt.WorkerOpts, l.mWorkerNetworkKeys[authority])
		if err != nil {
			return committeePrimariesCFG{}, fmt.Errorf("failed to create worker cfgs: %w", err)
		}

		committee.Authorities[authority] = AuthorityCFG{
			PrimaryAddress: primAddr,
			GRPC:           grpcAddr,
			NetworkKey:     l.mPrimaryNetworkKeys[authority],
			Stake:          1,
			Workers:        workerCFGs,
		}
	}

	return committee, nil
}

func strOrDef(in, defValue string) string {
	if in == "" {
		return defValue
	}
	return in
}

func newPrimaryCFG(portFact *portFactory, host string, opt NarwhalOpt) (Multiaddr, Multiaddr, error) {
	ports, err := portFact.newRandomPorts(2)
	if err != nil {
		return "", "", fmt.Errorf("failed to create primary cfg: %w", err)
	}

	prim2PrimAddr := newNarwhalMultiAddr(strOrDef(opt.PrimHost, host), strOrDef(opt.PrimPort, ports[0]))
	grpcAddr := newNarwhalMultiAddr(strOrDef(opt.GRPCHost, host), strOrDef(opt.GRPCPort, ports[1]))

	return prim2PrimAddr, grpcAddr, nil
}

func newWorkerCFGs(portFact *portFactory, host string, numWorkers int, workerOpts []NarwhalWorkerOpt, mWorkerNetworkKeys map[int]string) (map[string]WorkerCFG, error) {
	if numOpts := len(workerOpts); numOpts > 0 {
		numWorkers = numOpts
	}

	workers := make(map[string]WorkerCFG, numWorkers)
	for i := 0; i < numWorkers; i++ {
		var workerOpt NarwhalWorkerOpt
		if i < len(workerOpts) {
			workerOpt = workerOpts[i]
		}
		cfg, err := newWorkerCFG(portFact, host, workerOpt, mWorkerNetworkKeys[i])
		if err != nil {
			return nil, fmt.Errorf("failed to create worker %d: %w", i, err)
		}
		workers[strconv.Itoa(i)] = cfg
	}
	return workers, nil
}

func newWorkerCFG(portFact *portFactory, host string, opt NarwhalWorkerOpt, networkKey string) (WorkerCFG, error) {
	ports, err := portFact.newRandomPorts(2)
	if err != nil {
		return WorkerCFG{}, err
	}

	workerAddr := newNarwhalMultiAddr(strOrDef(opt.WorkerHost, host), strOrDef(opt.WorkerPort, ports[0]))
	txsAddr := newNarwhalMultiAddr(strOrDef(opt.TxsHost, host), strOrDef(opt.TxsPort, ports[1]))

	cfg := WorkerCFG{
		WorkerAddr:   workerAddr,
		Transactions: txsAddr,
		NetworkKey:   networkKey,
	}

	return cfg, nil
}

func writeParametersFile(filename, grpcAddr string, batchSize, headerSize int, batchDelay, headerDelay time.Duration) error {
	// contents pulled from mystenlabs/narwhal demo
	tmpl := `
{
    "batch_size": %d,
    "block_synchronizer": {
        "certificates_synchronize_timeout": "2_000ms",
        "handler_certificate_deliver_timeout": "2_000ms",
        "payload_availability_timeout": "2_000ms",
        "payload_synchronize_timeout": "2_000ms"
    },
    "consensus_api_grpc": {
        "get_collections_timeout": "5_000ms",
        "remove_collections_timeout": "5_000ms",
        "socket_addr": "%s"
    },
    "gc_depth": 50,
    "header_size": %d,
    "max_batch_delay": "%s",
    "max_concurrent_requests": 500000,
    "max_header_delay": "%s",
	"network_admin_server": {
		"primary_network_admin_server_port": 0,
		"worker_network_admin_server_base_port": 0
	},
    "prometheus_metrics": {
        "socket_addr": "/ip4/127.0.0.1/tcp/0/http"
    },
    "sync_retry_delay": "10_000ms",
    "sync_retry_nodes": 3
}`
	contents := fmt.Sprintf(tmpl, batchSize, grpcAddr, headerSize, batchDelay, headerDelay)
	return os.WriteFile(filename, []byte(contents), os.ModePerm)
}

func readNarwhalKeyFile(filename string) (string, error) {
	bb, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}

	var keyFile struct {
		Name string `json:"name"`
	}
	err = json.Unmarshal(bb, &keyFile)
	if err != nil {
		return "", err
	}
	return keyFile.Name, nil
}

func newNarwhalMultiAddr(host, port string) Multiaddr {
	return Multiaddr(path.Join("/ip4", host, "tcp", port, "http"))
}

var (
	primaryGRPCReady = []byte("Consensus API gRPC Server listening on")
	narwhalNodeReady = []byte("successfully booted on ")
	failedStart      = []byte("Caused by")
)

func narwhalLineCheckFn(b []byte, nodeType string) string {
	isFailedStart := bytes.Contains(b, failedStart)

	isReady := nodeType == "primary" && bytes.Contains(b, primaryGRPCReady) ||
		nodeType == "primary" && bytes.Contains(b, narwhalNodeReady) ||
		nodeType == "worker" && bytes.Contains(b, narwhalNodeReady)

	var status string
	switch {
	case isReady:
		status = "ready"
	case isFailedStart:
		status = "failed start"
	}
	return status
}

func newNarwhalReadyWriter(ctx context.Context, stream chan<- readyMsg, nodeType, nodeName, workerID string) *readyTailWriter {
	return newReadyTailWriter(ctx, stream, nodeType, nodeName, workerID, narwhalLineCheckFn)

}

func readCommitteeFiles(dirs testDirs) (committeePrimariesCFG, error) {
	var primCFGs committeePrimariesCFG
	err := readJSONFile(dirs.committeePrimaryFile(), &primCFGs)
	if err != nil {
		return committeePrimariesCFG{}, fmt.Errorf("failed to read primary committe file: %w", err)
	}

	return primCFGs, nil
}

func readJSONFile(filename string, v interface{}) error {
	bb, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	return json.Unmarshal(bb, v)
}

func isSignalKilledErr(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "signal: killed")
}
