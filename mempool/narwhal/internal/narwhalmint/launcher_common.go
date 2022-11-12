package narwhalmint

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func TestDir(now time.Time) string {
	return filepath.Join(os.ExpandEnv("$PWD/test_results"), TimeFilepath(now))
}

func TimeFilepath(now time.Time) string {
	return strings.ReplaceAll(now.Truncate(time.Millisecond).Format(time.Stamp), " ", "-")
}

type testDirs struct {
	rootDir   string
	nodeNames []string
}

func (t testDirs) committeePrimaryFile() string {
	return filepath.Join(t.rootDir, "primaries.json")
}

func (t testDirs) committeeWorkerFile() string {
	return filepath.Join(t.rootDir, "workers.json")
}

func (t testDirs) configDir(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "config")
}

func (t testDirs) configFile(nodeName string) string {
	return filepath.Join(t.configDir(nodeName), "config.toml")
}

func (t testDirs) dataDir(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "data")
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

func (t testDirs) nodeNetworkPrimaryKeyFile(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "primary_network_key.json")
}

func (t testDirs) nodeNetworkWorkerKeyFile(nodeName, workerID string) string {
	return filepath.Join(t.nodeDir(nodeName), "worker_"+workerID+"_network_key.json")
}

func (t testDirs) nodeNetworkWorkerKeyFiles(nodeName string, workerIDs []string) string {
	var files []string
	for _, workerID := range workerIDs {
		files = append(files, t.nodeNetworkWorkerKeyFile(nodeName, workerID))
	}
	return strings.Join(files, ",")
}

func (t testDirs) nodeParameterPrimaryFile(nodeName string) string {
	return filepath.Join(t.nodeDir(nodeName), "parameters_primary.json")
}

func (t testDirs) nodeParameterWorkerFile(nodeName, workerID string) string {
	return filepath.Join(t.nodeDir(nodeName), "parameters_worker_"+workerID+".json")
}

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
	checkFn  func(b []byte, nodeType string) string

	done        <-chan struct{}
	readyStream chan<- readyMsg
	status      string
}

func newReadyTailWriter(
	ctx context.Context,
	readyStream chan<- readyMsg,
	nodeType, nodeName, workerID string,
	checkFn func([]byte, string) string,
) *readyTailWriter {
	return &readyTailWriter{
		checkFn:     checkFn,
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

	r.status = r.checkFn(b, r.nodeType)
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

func awaitFn(ctx context.Context, readyMsgStream <-chan readyMsg, msgFn func(msg readyMsg) (bool, error)) <-chan error {
	startupCompleteStream := make(chan error)
	go func() {
		var startupErrs []error
		defer func() {
			if len(startupErrs) > 0 {
				startupCompleteStream <- newMultiErr("failed to startup node(s)", startupErrs)
			}
			close(startupCompleteStream)
		}()

		for {
			select {
			case <-ctx.Done():
			case msg := <-readyMsgStream:
				isEnd, err := msgFn(msg)
				if err != nil {
					startupErrs = append(startupErrs, err)
				}
				if isEnd {
					return
				}
			}
		}
	}()

	return startupCompleteStream
}

func newLogFileWriter(filename string) (io.Writer, func() error, error) {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	if os.IsNotExist(err) {
		f, err = os.Create(filename)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open log file: %w", err)
	}

	buf := bufio.NewWriter(f)
	closeFn := func() error {
		buf.Flush()
		return f.Close()
	}

	return buf, closeFn, nil
}

type portFactory struct {
	closers []io.Closer
}

func (p *portFactory) newRandomPorts(numPorts int) ([]string, error) {
	ports := make([]string, 0, numPorts)
	for i := 0; i < numPorts; i++ {
		port, err := p.newRandomPort()
		if err != nil {
			return nil, fmt.Errorf("failed to get host random port: %w", err)
		}
		ports = append(ports, port)
	}
	return ports, nil
}

func (p *portFactory) newRandomPort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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

func newTestResultsDir(now time.Time, paths ...string) testDirs {
	basePaths := append([]string{TestDir(now)}, paths...)
	return testDirs{
		rootDir: filepath.Join(basePaths...),
	}
}

func createDirs(dirs ...string) error {
	for _, subDir := range dirs {
		err := os.MkdirAll(subDir, dirPerm)
		if err != nil {
			return fmt.Errorf("failed to create subdir %s: %w", subDir, err)
		}
	}
	return nil
}

func newMultiErr(msg string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	errMsgs := make([]string, 0, len(errs))
	for _, e := range errs {
		errMsgs = append(errMsgs, e.Error())
	}

	return fmt.Errorf("%s:\n\t* %s", msg, strings.Join(errMsgs, "\n\t* "))
}
