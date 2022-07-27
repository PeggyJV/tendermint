package narwhalc

import (
	"context"

	"github.com/tendermint/tendermint/libs/bytes"
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

// Txs returns all txs via walking the DAG from teh RootCert and combines
// txs from the ExtraCerts.
func (d DAGCerts) Txs(ctx context.Context) (types.Txs, error) {
	if d.client == nil {
		return nil, nil
	}
	return d.client.DAGCertTXs(ctx, d)
}

func (d DAGCerts) DecorateBlock(ctx context.Context, block *types.Block) {
	block.Data.Certificates = &types.DAGCerts{
		RootCert: bytes.HexBytes(d.RootCert),
	}

	if len(d.ExtraCerts) > 0 {
		bzextra := make([]bytes.HexBytes, len(d.ExtraCerts))
		for i := range d.ExtraCerts {
			bzextra[i] = bytes.HexBytes(d.ExtraCerts[i])
		}
		block.Data.Certificates.ExtraCerts = bzextra
	}

	// Note: here we are setting the hash when we have both the transactions
	//		 alongside the certs. After the hash is set, we then set the txs
	//		 to nil. We do not want to transmit the txs p2p, buth rather just
	//		 the certificates and then the nodes will hydrate them as needed.
	//		 It may be better to have a separate block type for narwhal
	//		 integrated mempools, but I'm hesitant to boil the ocean. If this
	//		 fails for reasons, then we can readjust.
	//
	{
		block.DataHash = nil
		block.Hash()
		block.Data.Txs = nil
	}
}
