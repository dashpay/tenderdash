package types

import (
	"math/rand"
	"sort"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
)

// RandValidatorSet returns a randomized validator set (size: +numValidators+),
// where each validator has the same default voting power.
func RandValidatorSet(n int) (*ValidatorSet, []PrivValidator) {
	return GenerateValidatorSet(NewValSetParam(crypto.GenProTxHashes(n)))
}

// ValSetParam is a structure of parameters to make a validator set
type ValSetParam struct {
	ProTxHash        ProTxHash
	VotingPower      int64
	ProposerPriority int64
}

// NewValSetParam returns a list of validator set parameters with for every proTxHashes
// with default voting power
func NewValSetParam(proTxHashes []crypto.ProTxHash) []ValSetParam {
	opts := make([]ValSetParam, len(proTxHashes))
	for i, proTxHash := range proTxHashes {
		opts[i] = ValSetParam{
			ProTxHash:   proTxHash,
			VotingPower: DefaultDashVotingPower,
		}
	}
	return opts
}

// GenerateValidatorSet generates a validator set and a list of private validators
func GenerateValidatorSet(valParams []ValSetParam) (*ValidatorSet, []PrivValidator) {
	var (
		n              = len(valParams)
		proTxHashes    = make([]crypto.ProTxHash, n)
		valz           = make([]*Validator, n)
		privValidators = make([]PrivValidator, n)
		valzOptsMap    = make(map[string]ValSetParam)
	)
	for i, opt := range valParams {
		proTxHashes[i] = opt.ProTxHash
		valzOptsMap[opt.ProTxHash.String()] = opt
	}
	orderedProTxHashes, privateKeys, thresholdPublicKey :=
		bls12381.CreatePrivLLMQDataOnProTxHashesDefaultThreshold(proTxHashes)
	quorumHash := crypto.RandQuorumHash()
	for i := 0; i < n; i++ {
		privValidators[i] = NewMockPVWithParams(
			privateKeys[i],
			orderedProTxHashes[i],
			quorumHash,
			thresholdPublicKey,
			false,
			false,
		)
		opt := valzOptsMap[orderedProTxHashes[i].String()]
		valz[i] = NewValidator(privateKeys[i].PubKey(), opt.VotingPower, orderedProTxHashes[i])
	}
	sort.Sort(PrivValidatorsByProTxHash(privValidators))
	return NewValidatorSet(valz, thresholdPublicKey, crypto.SmallQuorumType(), quorumHash, true), privValidators
}

func WithPriority(valParams []ValSetParam) []ValSetParam {
	n := len(valParams)
	for i := 0; i < n; i++ {
		valParams[i].ProposerPriority = rand.Int63() % (MaxTotalVotingPower - (int64(n) * DefaultDashVotingPower))
	}
	return valParams
}
