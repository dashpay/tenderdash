package kvstore

import (
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash/llmq"
	tmtypes "github.com/dashpay/tenderdash/types"
)

// RandValidatorSetUpdate returns a list of cnt validators for initializing
// the application. Note that the keys are deterministically
// derived from the index in the array
func RandValidatorSetUpdate(cnt int) types.ValidatorSetUpdate {
	ld := llmq.MustGenerate(crypto.RandProTxHashes(cnt))
	vsu, err := types.LLMQToValidatorSetProto(
		*ld,
		types.WithNodeAddrs(randNodeAddrs(cnt)),
		types.WithRandQuorumHash(),
	)
	if err != nil {
		panic(err)
	}
	return *vsu
}

func randNodeAddrs(n int) []string {
	addrs := make([]string, n)
	for i := 0; i < n; i++ {
		addrs[i] = tmtypes.RandValidatorAddress().String()
	}
	return addrs
}
