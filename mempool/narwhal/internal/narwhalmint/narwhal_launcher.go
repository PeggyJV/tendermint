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

// Launcher is can set up and run a narwhal cluster.
type Launcher struct {
	BlockSizeLimitBytes int
	Host                string
	Out                 io.Writer
	Primaries           int // will default to 4 when not set
	Workers             int // will default to 1 when not set

	dirs             testDirs
	committeeCFG     committeeCFG
	runtimeErrStream <-chan error
	status           string
}

func (l *Launcher) NarwhalMempoolConfigs() []config.NarwhalMempoolConfig {
	out := make([]config.NarwhalMempoolConfig, 0, len(l.committeeCFG.Authorities))
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
		out = append(out, cfg)
	}
	return out
}

// Dir is the root directory for the narwhal nodes to execute from.
func (l *Launcher) Dir() string {
	return l.dirs.rootDir
}

// SetupFS sets up the filesystem to run the narwhal nodes.
func (l *Launcher) SetupFS(ctx context.Context, now time.Time) (e error) {
	if l.Primaries == 0 {
		l.Primaries = 4
		l.println("setting primaries to default value of 4")
	}
	if l.Workers == 0 {
		l.Workers = 1
		l.println("setting workers to default value of 1")
	}

	defer func() {
		if e != nil {
			os.RemoveAll(l.Dir())
		}
	}()

	err := l.setupTestEnv(ctx, now)
	if err != nil {
		return err
	}
	l.status = "setup"

	return nil
}

// Start will start all the narwhal nodes. This will start up the nodes in separate
// go routines. The context can be provided to control stopping the running cluster.
// Additionally, calling Stop will also stop the cluster. When Start returns the nodes
// are in fully operational.
func (l *Launcher) Start(ctx context.Context) error {
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
func (l *Launcher) NodeRuntimeErrs() <-chan error {
	return l.runtimeErrStream
}

func (l *Launcher) runAllNodes(ctx context.Context) (<-chan error, error) {
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
		for _, nodeName := range l.dirs.nodeNames {
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(idx int, nName string) {
					defer wg.Done()

					err := runWorker(ctx, readyMsgStream, l.dirs, nName, strconv.Itoa(idx))
					if err != nil && err != context.Canceled && !isSignalKilledErr(err) {
						errStream <- err
					}
				}(i, nodeName)
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

func isSignalKilledErr(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "signal: killed")
}

func (l *Launcher) awaitStartup(ctx context.Context, readyMsgStream <-chan readyMsg) <-chan error {
	startupCompleteStream := make(chan error)
	go func() {
		var startupErrs []error
		defer func() {
			if len(startupErrs) > 0 {
				startupCompleteStream <- newMultiErr("failed to startup narwhal_node(s)", startupErrs)
			}
			close(startupCompleteStream)
		}()

		nodes := make(map[string]struct{})
		for nodeName, authCFG := range l.committeeCFG.Authorities {
			nodes[nodeName] = struct{}{}
			for workerID := range authCFG.Workers {
				nodes[nodeName+"_worker_"+workerID] = struct{}{}
			}
		}

		for {
			select {
			case <-ctx.Done():
			case msg := <-readyMsgStream:
				key := msg.nodeName
				if msg.nodeType == "worker" && msg.workerID != "" {
					key += "_worker_" + msg.workerID
				}
				delete(nodes, key)

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
					err := fmt.Errorf("failed start for node(%s)%s: see %s for logs", msg.nodeName, workerDesc, logFile)
					startupErrs = append(startupErrs, err)
				}
				if len(nodes) == 0 {
					return
				}
			}
		}
	}()

	return startupCompleteStream
}

func (l *Launcher) setupTestEnv(ctx context.Context, now time.Time) (e error) {
	testingDir := filepath.Join(os.ExpandEnv("$PWD/test_results"), strings.ReplaceAll(now.Format(time.Stamp), " ", "-"))

	l.dirs = testDirs{
		rootDir: testingDir,
	}

	for _, subDir := range []string{l.dirs.nodesDir()} {
		err := os.MkdirAll(subDir, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create subdir %s: %w", subDir, err)
		}
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

func (l *Launcher) writeParameterFiles() error {
	for nodeName, auth := range l.committeeCFG.Authorities {
		paramFile := l.dirs.nodeParameterFile(nodeName)
		err := writeParametersFile(paramFile, string(auth.Primary.GRPC))
		if err != nil {
			return fmt.Errorf("failed to write parameters file(%s): %w", paramFile, err)
		}
	}
	return nil
}

func (l *Launcher) println(format string, args ...interface{}) {
	if l.Out == nil {
		return
	}
	fmt.Fprintln(l.Out, append([]interface{}{format}, args...)...)
}

type testDirs struct {
	rootDir   string
	nodeNames []string
}

func (t testDirs) committeeFile() string {
	return filepath.Join(t.rootDir, "committee.json")
}

func (t testDirs) nodesDir() string {
	return filepath.Join(t.rootDir, "nodes")
}

func (t testDirs) nodeDir(nodeName string) string {
	return filepath.Clean(filepath.Join(t.nodesDir(), strings.ReplaceAll(nodeName, "/", "_")))
}

func (t testDirs) nodeDBDir(nodeName, label string) string {
	return filepath.Join(t.nodeDir(nodeName), "dbs", label)
}

func (t testDirs) nodeKeyFile(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "key.json")
}

func (t testDirs) nodeLogDir(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "logs")
}

func (t testDirs) nodeLogFile(nodeName, label string) string {
	return filepath.Join(t.nodeLogDir(nodeName), label)
}

func (t testDirs) nodeParameterFile(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "parameters.json")
}

func runPrimary(ctx context.Context, readyStream chan<- readyMsg, dirs testDirs, nodeName string) error {
	label := "primary"

	readyTailer := newReadyTailWriter(ctx, readyStream, "primary", nodeName, "")
	err := runNodeExecCmd(ctx, readyTailer, dirs, nodeName, label, "primary", "--consensus-disabled")
	if err != nil {
		return fmt.Errorf("node %s encountered runtime error: %w", nodeName, err)
	}
	return nil
}

func runWorker(ctx context.Context, readyStream chan<- readyMsg, dirs testDirs, nodeName string, workerID string) error {
	label := "worker_" + workerID

	readyTailer := newReadyTailWriter(ctx, readyStream, "worker", nodeName, workerID)
	err := runNodeExecCmd(ctx, readyTailer, dirs, nodeName, label, "worker", "--id", workerID)
	if err != nil {
		return fmt.Errorf("node %s worker %s encountered runtime error: %w", nodeName, workerID, err)
	}
	return nil
}

func runNodeExecCmd(ctx context.Context, readyTailer io.Writer, dirs testDirs, nodeName, label string, subCmd string, subCmdArgs ...string) error {
	f, err := os.Create(dirs.nodeLogFile(nodeName, label))
	if err != nil {
		return fmt.Errorf("failed to create log file for node %s: %w", nodeName, err)
	}
	defer f.Close()

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

	err = os.MkdirAll(testDir.nodeLogDir(nodeName), os.ModePerm)
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
	err = os.MkdirAll(nodeDir, os.ModePerm)
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

func writeParametersFile(filename string, grpcAddr string) error {
	// contents pulled from mystenlabs/narwhal demo
	tmpl := `
{
    "batch_size": 500,
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
    "header_size": 250,
    "max_batch_delay": "200ms",
    "max_concurrent_requests": 500000,
    "max_header_delay": "500ms",
    "prometheus_metrics": {
        "socket_addr": "/ip4/127.0.0.1/tcp/0/http"
    },
    "sync_retry_delay": "10_000ms",
    "sync_retry_nodes": 3
}`
	contents := fmt.Sprintf(tmpl, grpcAddr)
	return os.WriteFile(filename, []byte(contents), os.ModePerm)
}

func newNarwhalMultiAddr(host, port string) Multiaddr {
	return Multiaddr(path.Join("/ip4", host, "tcp", port, "http"))
}

var (
	primaryGRPCReady = []byte("Consensus API gRPC Server listening on")
	narwhalNodeReady = []byte("successfully booted on ")
	failedStart      = []byte("Caused by")
)

type readyMsg struct {
	nodeType string
	nodeName string
	workerID string
	status   string
}

type readyTailWriter struct {
	nodeType string
	nodeName string
	workerID string

	done        <-chan struct{}
	readyStream chan<- readyMsg
	status      string
}

func newReadyTailWriter(ctx context.Context, readyStream chan<- readyMsg, nodeType, nodeName, workerID string) *readyTailWriter {
	return &readyTailWriter{
		nodeType:    nodeType,
		nodeName:    nodeName,
		workerID:    workerID,
		done:        ctx.Done(),
		readyStream: readyStream,
	}
}

func (r *readyTailWriter) Write(b []byte) (n int, err error) {
	if r.status != "" {
		return io.Discard.Write(b)
	}

	isFailedStart := bytes.Contains(b, failedStart)

	isReady := r.nodeType == "primary" && bytes.Contains(b, primaryGRPCReady) ||
		r.nodeType == "primary" && bytes.Contains(b, narwhalNodeReady) ||
		r.nodeType == "worker" && bytes.Contains(b, narwhalNodeReady)

	switch {
	case isReady:
		r.status = "ready"
	case isFailedStart:
		r.status = "failed start"
	}
	if r.status != "" {
		r.sendReady()
	}

	return io.Discard.Write(b)
}

func (r *readyTailWriter) sendReady() {
	msg := readyMsg{
		nodeType: r.nodeType,
		nodeName: r.nodeName,
		workerID: r.workerID,
		status:   r.status,
	}

	select {
	case <-r.done:
	case r.readyStream <- msg:
	}
}

type portFactory struct {
	closers []io.Closer
}

func (p *portFactory) newRandomPorts(numPorts int, host string) ([]string, error) {
	ports := make([]string, 0, numPorts)
	for i := 0; i < numPorts; i++ {
		port, err := p.newRandomPort(host)
		if err != nil {
			return nil, fmt.Errorf("failed to get host random port: %w", err)
		}
		ports = append(ports, port)
	}
	return ports, nil
}

func (p *portFactory) newRandomPort(host string) (string, error) {
	listener, err := net.Listen("tcp", host+":0")
	if err != nil {
		return "", fmt.Errorf("failed to take random port: %w", err)
	}
	// do not close here, so we don't accidentally get same ports across the bunch
	addr := listener.Addr().String() // form of: 127.0.0.1:48013

	p.closers = append(p.closers, listener)
	return strings.SplitAfter(addr, ":")[1], nil
}

func (p *portFactory) close() {
	for _, cl := range p.closers {
		_ = cl.Close()
	}
}

func newMultiErr(msg string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	errMsgs := make([]string, 0, len(errs))
	for _, e := range errs {
		errMsgs = append(errMsgs, e.Error())
	}

	return fmt.Errorf("%s:\n\t%s", msg, strings.Join(errMsgs, "\n\t* "))
}
