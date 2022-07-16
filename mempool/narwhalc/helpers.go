package narwhalc

import (
	"encoding/base64"

	"github.com/tendermint/tendermint/types"
)

// txID is a hash of the Tx.
func txID(tx []byte) []byte {
	return types.Tx(tx).Hash()
}

func base64Encode(src []byte) string {
	return base64.StdEncoding.EncodeToString(src)
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
