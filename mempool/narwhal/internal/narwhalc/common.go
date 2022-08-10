package narwhalc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/mempool/narwhal/internal/narwhalproto"
	"github.com/tendermint/tendermint/types"
)

// NodeMeta identifies common metadata for any given node (primary or worker).
type NodeMeta struct {
	Name  string
	Label string
	Type  string
	Addr  string
}

// String pretty prints the node metadata.
func (n NodeMeta) String() string {
	var (
		format []string
		args   []interface{}
	)
	if n.Label != "" {
		format, args = append(format, "%s"), append(args, n.Label)
	}
	format, args = []string{"%s"}, []interface{}{n.Type}
	format, args = append(format, "node(%s)"), append(args, n.Name)
	format, args = append(format, "at addr (%s)"), append(args, n.Addr)
	return fmt.Sprintf(strings.Join(format, " "), args...)
}

type clientBase struct {
	meta NodeMeta
}

// Meta returns the meta of the node.
func (t clientBase) Meta() NodeMeta {
	return t.meta
}

// txFull is a wrapped tx. This tx will be propagated throughout the narwhal cluster.
// TODO(berg): replace this with a proto message
type txFull struct {
	GasWanted int64    `gob:"gas_wanted"`
	Tx        types.Tx `gob:"tx"`
}

func hexBytesToProtoCert(cert tmbytes.HexBytes) *narwhalproto.CertificateDigest {
	return &narwhalproto.CertificateDigest{Digest: cert}
}

func protoCertDigestToHexBytes(cert *narwhalproto.CertificateDigest) tmbytes.HexBytes {
	return cert.Digest
}

func newGRPCConnection(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  1.0 * time.Second,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   60 * time.Second,
			},
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	return grpc.DialContext(ctx, addr, opts...)
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

func mapSlice[In, Out any](in []In, fn func(In) Out) []Out {
	var out []Out
	if in != nil {
		out = make([]Out, 0, len(in))
	}
	for i := range in {
		out = append(out, fn(in[i]))
	}
	return out
}
