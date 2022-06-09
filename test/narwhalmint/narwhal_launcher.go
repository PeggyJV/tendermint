package narwhalmint

import (
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
)

// LauncherNarwhal is can set up and run a narwhal cluster.
type LauncherNarwhal struct {
	Host              string
	ParameterContents string
	Primaries         int // will default to 4 when not set
	Workers           int // will default to 1 when not set

	Out io.Writer

	dirs         testDirs
	committeeCFG committeeCFG
	isSetup      bool
}

// RunNarwhalNodes executes the narwhal nodes. This call does not block. The returned channel
// will close when all nodes have stopped executing. When the ctx is canceled, the narwhal
// nodes will indeed terminate.
func (l *LauncherNarwhal) RunNarwhalNodes(ctx context.Context) <-chan error {
	if !l.isSetup {
		stream := make(chan error, 1)
		stream <- fmt.Errorf("the filesystem is not setup to run nodes; make sure to call SetupFS before running the nodes")
		return stream
	}

	return l.runAllNodes(ctx)
}

// SetupFS sets up the filesystem to run the narwhal nodes.
func (l *LauncherNarwhal) SetupFS(ctx context.Context, now time.Time) (e error) {
	if l.ParameterContents == "" {
		return fmt.Errorf("ParameterContents must be set to run narwhal nodes")
	}
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
			os.RemoveAll(l.TestRootDir())
		}
	}()

	err := l.setupTestEnv(ctx, now)
	if err != nil {
		return err
	}
	l.isSetup = true

	return nil
}

// TestRootDir is the root directory for the narwhal nodes to execute from.
func (l *LauncherNarwhal) TestRootDir() string {
	return l.dirs.rootDir
}

func (l *LauncherNarwhal) runAllNodes(ctx context.Context) <-chan error {
	stream := make(chan error)

	go func(workers int) {
		defer close(stream)

		wg := new(sync.WaitGroup)
		// setup primaries
		for _, nodeName := range l.dirs.nodeNames {
			wg.Add(1)
			go func(nName string) {
				defer wg.Done()

				err := runPrimary(ctx, l.dirs, nName)
				if err != nil && err != context.Canceled {
					stream <- err
				}
			}(nodeName)
		}

		// setup workers
		for _, nodeName := range l.dirs.nodeNames {
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(idx int, nName string) {
					defer wg.Done()

					err := runWorker(ctx, l.dirs, nName, strconv.Itoa(idx))
					if err != nil && err != context.Canceled {
						stream <- err
					}
				}(i, nodeName)
			}
		}
		wg.Wait()
	}(l.Workers)

	return stream
}

func (l *LauncherNarwhal) println(format string, args ...interface{}) {
	if l.Out == nil {
		return
	}
	fmt.Fprintln(l.Out, append([]interface{}{format}, args...)...)
}

func (l *LauncherNarwhal) setupTestEnv(ctx context.Context, now time.Time) (e error) {
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

	err = writeParametersFile(l.dirs.parameterFile(), l.ParameterContents)
	if err != nil {
		return fmt.Errorf("failed to write paramters file: %w", err)
	}

	return nil
}

type testDirs struct {
	rootDir   string
	nodeNames []string
}

func (t testDirs) committeeFile() string {
	return filepath.Join(t.rootDir, "committee.json")
}

func (t testDirs) parameterFile() string {
	return filepath.Join(t.rootDir, "parameters.json")
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
	return filepath.Join(t.nodeDir(nodeName), "keys.json")
}

func (t testDirs) nodeLogDir(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "logs")
}

func (t testDirs) nodeLogFile(nodeName, label string) string {
	return filepath.Join(t.nodeLogDir(nodeName), label)
}

func runPrimary(ctx context.Context, dirs testDirs, nodeName string) error {
	label := "primary"

	err := runExecCmd(ctx, dirs, nodeName, label, "primary", "--consensus-disabled")
	if err != nil {
		return fmt.Errorf("node %s encountered runtime error: %w", nodeName, err)
	}
	return nil
}

func runWorker(ctx context.Context, dirs testDirs, nodeName string, workerID string) error {
	label := "worker_" + workerID

	err := runExecCmd(ctx, dirs, nodeName, label, "worker", "--id", workerID)
	if err != nil {
		return fmt.Errorf("node %s worker %s encountered runtime error: %w", nodeName, workerID, err)
	}
	return nil
}

func runExecCmd(ctx context.Context, dirs testDirs, nodeName, label string, subCmd string, subCmdArgs ...string) error {
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
		"--parameters", dirs.parameterFile(),
		"--store", dirs.nodeDBDir(nodeName, label),
	}
	args = append(args, subCmd)
	args = append(args, subCmdArgs...)
	cmd := exec.CommandContext(ctx, "narwhal_node", args...)
	cmd.Stdout, cmd.Stderr = f, f

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
		Authorities map[string]authorityCFG `json:"authorities"`
	}

	authorityCFG struct {
		Primary primaryCFG           `json:"primary"`
		Stake   int                  `json:"stake"`
		Workers map[string]workerCFG `json:"workers"`
	}

	primaryCFG struct {
		PrimaryToPrimary string `json:"primary_to_primary"`
		WorkerToPrimary  string `json:"worker_to_primary"`
	}

	workerCFG struct {
		PrimaryToWorker string `json:"primary_to_worker"`
		Transactions    string `json:"transactions"`
		WorkerToWorker  string `json:"worker_to_worker"`
	}
)

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
		Authorities: make(map[string]authorityCFG, len(nodeNames)),
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

		committee.Authorities[authority] = authorityCFG{
			Primary: primCFG,
			Stake:   1,
			Workers: workerCFGs,
		}
	}

	return committee, nil
}

func newPrimaryCFG(portFact *portFactory, host string) (primaryCFG, error) {
	ports, err := portFact.newRandomPorts(2, host)
	if err != nil {
		return primaryCFG{}, fmt.Errorf("failed to create primary cfg: %w", err)
	}

	cfg := primaryCFG{
		PrimaryToPrimary: newNarwhalMultiAddr(host, ports[0]),
		WorkerToPrimary:  newNarwhalMultiAddr(host, ports[1]),
	}

	return cfg, nil
}

func newWorkerCFGs(portFact *portFactory, host string, numWorkers int) (map[string]workerCFG, error) {
	workers := make(map[string]workerCFG, numWorkers)
	for i := 0; i < numWorkers; i++ {
		cfg, err := newWorkerCFG(portFact, host)
		if err != nil {
			return nil, fmt.Errorf("failed to create worker %d: %w", i, err)
		}
		workers[strconv.Itoa(i)] = cfg
	}
	return workers, nil
}

func newWorkerCFG(portFact *portFactory, host string) (workerCFG, error) {
	ports, err := portFact.newRandomPorts(3, host)
	if err != nil {
		return workerCFG{}, err
	}

	cfg := workerCFG{
		PrimaryToWorker: newNarwhalMultiAddr(host, ports[0]),
		Transactions:    newNarwhalMultiAddr(host, ports[1]),
		WorkerToWorker:  newNarwhalMultiAddr(host, ports[2]),
	}

	return cfg, nil
}

func writeParametersFile(filename string, contents string) error {
	return os.WriteFile(filename, []byte(contents), os.ModePerm)
}

func newNarwhalMultiAddr(host, port string) string {
	return path.Join("/ip4", host, "tcp", port, "http")
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
