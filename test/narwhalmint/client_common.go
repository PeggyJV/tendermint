package narwhalmint

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

// NodeMeta identifies common metadata for any given node (primary or worker).
type NodeMeta struct {
	NodeName string
	WorkerID string
	Addr     string
}

// String pretty prints the node metadata.
func (n NodeMeta) String() string {
	format, args := []string{"node(%s)"}, []interface{}{n.NodeName}
	if n.WorkerID != "" {
		format, args = append(format, "workers(%s)"), append(args, n.WorkerID)
	}
	format, args = append(format, "at addr(%s)"), append(args, n.Addr)
	return fmt.Sprintf(strings.Join(format, " "), args...)
}

type clientBase struct {
	meta NodeMeta
}

// Meta returns the meta of the node.
func (t clientBase) Meta() NodeMeta {
	return t.meta
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
