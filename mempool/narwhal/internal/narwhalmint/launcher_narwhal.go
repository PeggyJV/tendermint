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
	BatchSize  int // will default to 5000
	HeaderSize int // will default to 500
	Host       string
	Out        io.Writer
	Primaries  int // will default to 4 when not set
	Workers    int // will default to 1 when not set

	dirs             testDirs
	committeeCFG     committeeCFG
	runtimeErrStream <-chan error
	status           string
}

func (l *LauncherNarwhal) NarwhalMempoolConfigs() []*config.NarwhalMempoolConfig {
	out := make([]*config.NarwhalMempoolConfig, 0, len(l.committeeCFG.Authorities))
	for pk, primary := range l.committeeCFG.Authorities {
		cfg := config.NarwhalMempoolConfig{
			PrimaryAddr:             primary.Primary.GRPC.HostPort(),
			PrimaryEncodedPublicKey: pk,
		}
		var i int
		for _, worker := range primary.Workers {
			cfg.Workers = append(cfg.Workers, config.NarwhalWorkerConfig{
				Name: strconv.Itoa(i),
				Addr: worker.Transactions.HostPort(),
			})
			i++
		}
		out = append(out, &cfg)
	}
	return out
}

// Dir is the root directory for the narwhal nodes to execute from.
func (l *LauncherNarwhal) Dir() string {
	return l.dirs.rootDir
}

// SetupFS sets up the filesystem to run the narwhal nodes.
func (l *LauncherNarwhal) SetupFS(ctx context.Context, now time.Time) error {
	if l.BatchSize == 0 {
		l.BatchSize = 5000
	}
	if l.HeaderSize == 0 {
		l.HeaderSize = 500
	}
	if l.Primaries == 0 {
		l.Primaries = 4
		l.println("setting primaries to default value of 4")
	}
	if l.Workers == 0 {
		l.Workers = 1
		l.println("setting workers to default value of 1")
	}

	err := l.setupTestEnv(ctx, now)
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

	l.committeeCFG, err = readCommitteeFile(l.dirs.committeeFile())
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
		// setup primaries
		for i := range l.dirs.nodeNames {
			wg.Add(1)
			go func(nName string) {
				defer wg.Done()

				err := runPrimary(ctx, readyMsgStream, l.dirs, nName)
				if err != nil && err != context.Canceled && !isSignalKilledErr(err) {
					errStream <- err
				}
			}(l.dirs.nodeNames[i])
		}

		// setup workers
		for i := range l.dirs.nodeNames {
			for j := 0; j < workers; j++ {
				wg.Add(1)
				go func(idx int, nName string) {
					defer wg.Done()

					err := runWorker(ctx, readyMsgStream, l.dirs, nName, strconv.Itoa(idx))
					if err != nil && err != context.Canceled && !isSignalKilledErr(err) {
						errStream <- err
					}
				}(j, l.dirs.nodeNames[i])
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

func (l *LauncherNarwhal) setupTestEnv(ctx context.Context, now time.Time) (e error) {
	l.dirs = newTestResultsDir(now, "narwhal")
	if err := createDirs(l.dirs.nodesDir()); err != nil {
		return err
	}

	nodeNames, err := setupNodes(ctx, l.dirs, l.Primaries)
	if err != nil {
		return fmt.Errorf("failed to setup narwhal node directories: %w", err)
	}
	l.dirs.nodeNames = nodeNames

	comCFG, err := setupCommitteeCFG(l.Host, l.dirs.nodeNames, l.Workers)
	if err != nil {
		return err
	}
	l.committeeCFG = comCFG

	err = writeCommitteeFile(l.dirs.committeeFile(), comCFG)
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
		err := writeParametersFile(paramFile, string(auth.Primary.GRPC), l.BatchSize, l.HeaderSize)
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

func runPrimary(ctx context.Context, readyStream chan<- readyMsg, dirs testDirs, nodeName string) error {
	label := "primary"

	readyTailer := newNarwhalReadyWriter(ctx, readyStream, "primary", nodeName, "")
	err := runNodeExecCmd(ctx, readyTailer, dirs, nodeName, label, "primary", "--consensus-disabled")
	if err != nil {
		return fmt.Errorf("node %s encountered runtime error: %w", nodeName, err)
	}
	return nil
}

func runWorker(ctx context.Context, readyStream chan<- readyMsg, dirs testDirs, nodeName string, workerID string) error {
	label := "worker_" + workerID

	readyTailer := newNarwhalReadyWriter(ctx, readyStream, "worker", nodeName, workerID)
	err := runNodeExecCmd(ctx, readyTailer, dirs, nodeName, label, "worker", "--id", workerID)
	if err != nil {
		return fmt.Errorf("node %s worker %s encountered runtime error: %w", nodeName, workerID, err)
	}
	return nil
}

func runNodeExecCmd(ctx context.Context, readyTailer io.Writer, dirs testDirs, nodeName, label string, subCmd string, subCmdArgs ...string) error {
	f, closeFn, err := newLogFileWriter(dirs.nodeLogFile(nodeName, label))
	if err != nil {
		return fmt.Errorf("failed to create log file for node %s: %w", nodeName, err)
	}
	defer closeFn()

	// the argument/flag intertwining is required by the node cli
	args := []string{
		"-vvv", // set logs to debug
		"run",  // first subcommand
		"--keys", dirs.nodeKeyFile(nodeName),
		"--committee", dirs.committeeFile(),
		"--parameters", dirs.nodeParameterFile(nodeName),
		"--store", dirs.nodeDBDir(nodeName, label),
	}
	args = append(args, subCmd)
	args = append(args, subCmdArgs...)
	cmd := exec.CommandContext(ctx, "narwhal_node", args...)
	cmd.Stdout, cmd.Stderr = f, io.MultiWriter(readyTailer, f)

	return cmd.Run()
}

func setupNodes(ctx context.Context, testDir testDirs, numPrimaries int) ([]string, error) {
	nodeNames := make([]string, 0, numPrimaries)
	for i := 0; i < numPrimaries; i++ {
		nodeName, err := setupNodeDir(ctx, testDir)
		if err != nil {
			return nil, fmt.Errorf("failed to setup node dir: %w", err)
		}
		nodeNames = append(nodeNames, nodeName)
	}
	return nodeNames, nil
}

func setupNodeDir(ctx context.Context, testDir testDirs) (string, error) {
	nodeName, err := newKeyFile(ctx, testDir)
	if err != nil {
		return "", fmt.Errorf("failed to create new key file: %w", err)
	}

	err = createDirs(testDir.nodeLogDir(nodeName))
	if err != nil {
		return "", fmt.Errorf("failed to create log dir for node %s: %w", nodeName, err)
	}

	return nodeName, nil
}

func newKeyFile(ctx context.Context, testDir testDirs) (string, error) {
	tmpFile := filepath.Join(testDir.nodesDir(), strconv.Itoa(rand.Int()))
	cmd := exec.CommandContext(ctx, "narwhal_node", "generate_keys", "--filename", tmpFile)
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute generate_keys: %w", err)
	}

	b, err := os.ReadFile(tmpFile)
	if err != nil {
		return "", fmt.Errorf("failed to read newly creatd key file: %w", err)
	}

	var keyFile struct {
		Name string `json:"name"`
	}
	err = json.Unmarshal(b, &keyFile)
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

type (
	committeeCFG struct {
		Authorities map[string]AuthorityCFG `json:"authorities"`
		Epoch       int                     `json:"epoch"`
	}

	AuthorityCFG struct {
		Primary PrimaryCFG           `json:"primary"`
		Stake   int                  `json:"stake"`
		Workers map[string]WorkerCFG `json:"workers"`
	}

	PrimaryCFG struct {
		PrimaryToPrimary Multiaddr `json:"primary_to_primary"`
		WorkerToPrimary  Multiaddr `json:"worker_to_primary"`
		GRPC             Multiaddr `json:"-"`
	}

	WorkerCFG struct {
		PrimaryToWorker Multiaddr `json:"primary_to_worker"`
		Transactions    Multiaddr `json:"transactions"`
		WorkerToWorker  Multiaddr `json:"worker_to_worker"`
	}
)

type Multiaddr string

func (m Multiaddr) HostPort() string {
	parts := strings.Split(string(m), "/")
	if len(parts) < 6 {
		return ""
	}
	return net.JoinHostPort(parts[2], parts[4])
}

func writeCommitteeFile(filename string, cfg committeeCFG) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to write comittee.json file: %w", err)
	}

	return os.WriteFile(filename, b, os.ModePerm)
}

func setupCommitteeCFG(host string, nodeNames []string, numWorkers int) (committeeCFG, error) {
	portFact := new(portFactory)
	defer portFact.close()

	committee := committeeCFG{
		Authorities: make(map[string]AuthorityCFG, len(nodeNames)),
	}
	for _, authority := range nodeNames {
		primCFG, err := newPrimaryCFG(portFact, host)
		if err != nil {
			return committeeCFG{}, fmt.Errorf("failed to create pimary cfg: %w", err)
		}

		workerCFGs, err := newWorkerCFGs(portFact, host, numWorkers)
		if err != nil {
			return committeeCFG{}, fmt.Errorf("failed to create worker cfgs: %w", err)
		}

		committee.Authorities[authority] = AuthorityCFG{
			Primary: primCFG,
			Stake:   1,
			Workers: workerCFGs,
		}
	}

	return committee, nil
}

func newPrimaryCFG(portFact *portFactory, host string) (PrimaryCFG, error) {
	ports, err := portFact.newRandomPorts(3, host)
	if err != nil {
		return PrimaryCFG{}, fmt.Errorf("failed to create primary cfg: %w", err)
	}

	cfg := PrimaryCFG{
		PrimaryToPrimary: newNarwhalMultiAddr(host, ports[0]),
		WorkerToPrimary:  newNarwhalMultiAddr(host, ports[1]),
		GRPC:             newNarwhalMultiAddr(host, ports[2]),
	}

	return cfg, nil
}

func newWorkerCFGs(portFact *portFactory, host string, numWorkers int) (map[string]WorkerCFG, error) {
	workers := make(map[string]WorkerCFG, numWorkers)
	for i := 0; i < numWorkers; i++ {
		cfg, err := newWorkerCFG(portFact, host)
		if err != nil {
			return nil, fmt.Errorf("failed to create worker %d: %w", i, err)
		}
		workers[strconv.Itoa(i)] = cfg
	}
	return workers, nil
}

func newWorkerCFG(portFact *portFactory, host string) (WorkerCFG, error) {
	ports, err := portFact.newRandomPorts(3, host)
	if err != nil {
		return WorkerCFG{}, err
	}

	cfg := WorkerCFG{
		PrimaryToWorker: newNarwhalMultiAddr(host, ports[0]),
		Transactions:    newNarwhalMultiAddr(host, ports[1]),
		WorkerToWorker:  newNarwhalMultiAddr(host, ports[2]),
	}

	return cfg, nil
}

func writeParametersFile(filename, grpcAddr string, batchSize, headerSize int) error {
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
    "max_batch_delay": "200ms",
    "max_concurrent_requests": 500000,
    "max_header_delay": "500ms",
    "prometheus_metrics": {
        "socket_addr": "/ip4/127.0.0.1/tcp/0/http"
    },
    "sync_retry_delay": "10_000ms",
    "sync_retry_nodes": 3
}`
	contents := fmt.Sprintf(tmpl, batchSize, grpcAddr, headerSize)
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

func readCommitteeFile(filename string) (committeeCFG, error) {
	bb, err := os.ReadFile(filename)
	if err != nil {
		return committeeCFG{}, err
	}

	var committee committeeCFG
	err = json.Unmarshal(bb, &committee)
	if err != nil {
		return committeeCFG{}, err
	}
	return committee, nil
}

func isSignalKilledErr(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "signal: killed")
}
