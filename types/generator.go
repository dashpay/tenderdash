package types

import (
	"context"
	"sort"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash/llmq"
)

// RandValidatorSet returns a randomized validator set (size: +numValidators+),
// where each validator has the same default voting power.
func RandValidatorSet(n int) (*ValidatorSet, []PrivValidator) {
	return GenerateValidatorSet(NewValSetParam(crypto.RandProTxHashes(n)))
}

// ValSetParam is a structure of parameters to make a validator set
type ValSetParam struct {
	ProTxHash   ProTxHash
	VotingPower int64
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

// ValSetOptions is the options for generation of validator set and private validators
type ValSetOptions struct {
	PrivValsMap      map[string]PrivValidator
	PrivValsAtHeight int64
}

// WithUpdatePrivValAt sets a list of the private validators to update validator set at passed the height
func WithUpdatePrivValAt(privVals []PrivValidator, height int64) func(opt *ValSetOptions) {
	return func(opt *ValSetOptions) {
		for _, pv := range privVals {
			proTxhash, _ := pv.GetProTxHash(context.Background())
			opt.PrivValsMap[proTxhash.String()] = pv
		}
		opt.PrivValsAtHeight = height
	}
}

// ValSetOptionFunc is a type of validator-set function to manage options of generator
type ValSetOptionFunc func(opt *ValSetOptions)

// GenerateValidatorSet generates a validator set and a list of private validators
func GenerateValidatorSet(valParams []ValSetParam, opts ...ValSetOptionFunc) (*ValidatorSet, []PrivValidator) {
	var (
		n              = len(valParams)
		proTxHashes    = make([]crypto.ProTxHash, n)
		valz           = make([]*Validator, 0, n)
		privValidators = make([]PrivValidator, 0, n)
		valzOptsMap    = make(map[string]ValSetParam)
	)
	for i, opt := range valParams {
		proTxHashes[i] = opt.ProTxHash
		valzOptsMap[opt.ProTxHash.String()] = opt
	}
	valSetOpts := ValSetOptions{
		PrivValsMap: make(map[string]PrivValidator),
	}
	for _, fn := range opts {
		fn(&valSetOpts)
	}
	ld := llmq.MustGenerate(proTxHashes)
	quorumHash := crypto.RandQuorumHash()
	mockPVFunc := newMockPVFunc(valSetOpts, quorumHash, ld.ThresholdPubKey)
	iter := ld.Iter()
	for iter.Next() {
		proTxHash, qks := iter.Value()
		opt := valzOptsMap[proTxHash.String()]
		privValidators = append(privValidators, mockPVFunc(proTxHash, qks.PrivKey))
		valz = append(valz, NewValidator(qks.PubKey, opt.VotingPower, proTxHash, ""))
	}
	sort.Sort(PrivValidatorsByProTxHash(privValidators))
	return NewValidatorSet(valz, ld.ThresholdPubKey, crypto.SmallQuorumType(), quorumHash, true), privValidators
}

// MakeGenesisValsFromValidatorSet converts ValidatorSet data into a list of GenesisValidator
func MakeGenesisValsFromValidatorSet(valz *ValidatorSet) []GenesisValidator {
	genVals := make([]GenesisValidator, len(valz.Validators))
	for i, val := range valz.Validators {
		genVals[i] = GenesisValidator{
			PubKey:    val.PubKey,
			Power:     DefaultDashVotingPower,
			ProTxHash: val.ProTxHash,
		}
	}
	return genVals
}

func newMockPVFunc(
	opts ValSetOptions,
	quorumHash crypto.QuorumHash,
	thresholdPubKey crypto.PubKey,
) func(crypto.ProTxHash, crypto.PrivKey) PrivValidator {
	return func(
		proTxHash crypto.ProTxHash,
		privKey crypto.PrivKey,
	) PrivValidator {
		privVal, ok := opts.PrivValsMap[proTxHash.String()]
		if ok && opts.PrivValsAtHeight > 0 {
			privVal.UpdatePrivateKey(context.Background(), privKey, quorumHash, thresholdPubKey, opts.PrivValsAtHeight)
			return privVal
		}
		return NewMockPVWithParams(
			privKey,
			proTxHash,
			quorumHash,
			thresholdPubKey,
			false,
			false,
		)
	}
}
