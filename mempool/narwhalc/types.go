package narwhalc

import (
	"context"

	"github.com/tendermint/tendermint/types"
)

// CertificateDigest represents a certificate digest of a collection/batch/list of TXs that compose
// list of TXs. Often referred to as collection ID in the narwhal world.
type CertificateDigest []byte

// DAGCerts represents the certificate digests from a mempool DAG. We traverse
// DAG starting from the RootCert, obtaining all the descendants from it. In
// addition, we make room for ExtraCerts, which will fit into the block, but are not reachable
// from the RootCert provided.
type DAGCerts struct {
	// RootCert represents the root cert to traverse the DAG.
	RootCert CertificateDigest

	// ExtraCerts represent additional certs that are not reachable from the RootCert
	// to be included in the block proposal. This is useful when you have the capacity
	// to add more certificates, are unable to add all certificates for the RootCert's
	// parent node.
	ExtraCerts []CertificateDigest

	client interface {
		DAGCertTXs(ctx context.Context, certs DAGCerts) (types.Txs, error)
	}
}

// TXs returns all TXs via walking the DAG from teh RootCert and combines
// TXs from the ExtraCerts.
func (d DAGCerts) TXs(ctx context.Context) (types.Txs, error) {
	if d.client == nil {
		return nil, nil
	}
	return d.client.DAGCertTXs(ctx, d)
}
