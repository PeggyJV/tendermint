package narwhalmint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

/*
To run teh tests simply run the normal go test .
You may parameterize the test setup by providing it an env var
to overwrite the default values. The env vars are listed with default
values in parens as follows:
	NARWHAL_WIPE (false)
	NARWHAL_PRIMARIES (4)
	NARWHAL_WORKERS (1)

Be mindful of overwriting the primaries, they should be able to reach consensus.
4 or 7 are good numbers for local testing. Scale the workers as you see fit.
*/
func TestNarwhalNetwork(t *testing.T) {
	shouldCleanupRaw := os.Getenv("NARWHAL_WIPE")
	shouldCleanup := shouldCleanupRaw == "true" || shouldCleanupRaw == "1" || shouldCleanupRaw == "yes"
	if shouldCleanup {
		t.Log("test will cleanup directory artifacts upon completion")
	}

	primaries := envOrIntDefault("NARWHAL_PRIMARIES", 4)
	workers := envOrIntDefault("NARWHAL_WORKERS", 1)
	t.Logf("configured with %d primaries with %d workers", primaries, workers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirs, err := setupTestEnv(t, ctx, time.Now(), primaries, workers, shouldCleanup)
	require.NoError(t, err)
	t.Log("created narwhal network stack at", dirs.rootDir)

	defer func() {
		if shouldCleanup {
			assert.NoError(t, os.RemoveAll(dirs.rootDir))
		}
	}()

	for i := 0; i < primaries; i++ {
		//err := runPrimaryNode(ctx, i, assets)
		//if err != nil {
		//	fmt.Printf("failed to create primary node %d: %s", i, err)
		//}
	}
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

func (t testDirs) nodeKeyFile(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "keys.json")
}

//func getGitRoot() (string, error) {}

func setupTestEnv(t *testing.T, ctx context.Context, now time.Time, primaries, workers int, cleanup bool) (testDirs, error) {
	testingDir := filepath.Join(os.ExpandEnv("$PWD/test_results"), strings.ReplaceAll(now.Format(time.Stamp), " ", "-"))

	assets := testDirs{
		rootDir: testingDir,
	}
	defer func() {
		if cleanup {
			assert.NoError(t, os.RemoveAll(assets.rootDir))
		}
	}()

	for _, subDir := range []string{assets.nodesDir()} {
		err := os.MkdirAll(subDir, os.ModePerm)
		require.NoErrorf(t, err, "failed to create dir %s: %s", subDir, err)
	}

	nodeNames, err := setupNodes(ctx, assets, primaries)
	require.NoError(t, err)
	assets.nodeNames = nodeNames

	err = setupMetaFiles(assets, workers)
	require.NoError(t, err)

	return assets, nil
}

func setupMetaFiles(dirs testDirs, workers int) error {
	err := writeCommitteeFile(dirs.committeeFile(), dirs.nodeNames, workers)
	if err != nil {
		return fmt.Errorf("failed to write committee file: %w", err)
	}

	err = writeParametersFile(dirs.parameterFile())
	if err != nil {
		return fmt.Errorf("failed to write paramters file: %w", err)
	}

	return nil
}

func setupNodes(ctx context.Context, testDir testDirs, numPrimaries int) ([]string, error) {
	nodeNames := make([]string, 0, numPrimaries)
	for i := 0; i < numPrimaries; i++ {
		nodeName, err := newKeyFile(ctx, testDir)
		if err != nil {
			return nil, fmt.Errorf("failed to create new key file: %w", err)
		}
		nodeNames = append(nodeNames, nodeName)
	}
	return nodeNames, nil
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

func writeCommitteeFile(filename string, nodeNames []string, numWorkers int) error {
	portFact := new(portFactory)
	defer portFact.close()

	host := "127.0.0.1"

	cfg, err := setupCommitteeCFG(portFact, host, nodeNames, numWorkers)
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to write comittee.json file: %w", err)
	}

	return os.WriteFile(filename, b, os.ModePerm)
}

func setupCommitteeCFG(portFact *portFactory, host string, nodeNames []string, numWorkers int) (committeeCFG, error) {
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

func writeParametersFile(filename string) error {
	// contents pulled from mystenlabs/narwhal demo
	contents := `
{
    "batch_size": 5,
    "block_synchronizer": {
        "certificates_synchronize_timeout": "2_000ms",
        "handler_certificate_deliver_timeout": "2_000ms",
        "payload_availability_timeout": "2_000ms",
        "payload_synchronize_timeout": "2_000ms"
    },
    "consensus_api_grpc": {
        "get_collections_timeout": "5_000ms",
        "remove_collections_timeout": "5_000ms",
        "socket_addr": "/ip4/0.0.0.0/tcp/0/http"
    },
    "gc_depth": 50,
    "header_size": 1000,
    "max_batch_delay": "200ms",
    "max_concurrent_requests": 500000,
    "max_header_delay": "2000ms",
    "sync_retry_delay": "10_000ms",
    "sync_retry_nodes": 3
}`

	return os.WriteFile(filename, []byte(contents), os.ModePerm)
}

func newNarwhalMultiAddr(host, port string) string {
	return path.Join("ip4", host, "tcp", port, "http")
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
		// this should really never fail.... if it does go booooom
		return "", fmt.Errorf("failed to take random port: %w", err)
	}
	// do not close here so we don't accidentally get same ports across the bunch
	addr := listener.Addr().String() // form of: 127.0.0.1:48013

	p.closers = append(p.closers, listener)
	return strings.SplitAfter(addr, ":")[1], nil
}

func (p *portFactory) close() {
	for _, cl := range p.closers {
		_ = cl.Close()
	}
}

func envOrIntDefault(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}

	i, _ := strconv.Atoi(strings.TrimSpace(v))
	return i
}
