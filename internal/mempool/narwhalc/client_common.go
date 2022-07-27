package narwhalc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tendermint/tendermint/internal/mempool/narwhalc/narwhalproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
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

func certDigestToProtoCertDigest(cert CertificateDigest) *narwhalproto.CertificateDigest {
	return &narwhalproto.CertificateDigest{Digest: cert}
}

func protoCertDigestToCertDigest(protoCert *narwhalproto.CertificateDigest) CertificateDigest {
	return protoCert.Digest
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
