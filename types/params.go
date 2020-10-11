package types

import (
	"errors"
	"fmt"
	"github.com/tendermint/tendermint/crypto"
	"time"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto/tmhash"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

const (
	// MaxBlockSizeBytes is the maximum permitted size of the blocks.
	MaxBlockSizeBytes = 104857600 // 100MB

	// BlockPartSizeBytes is the size of one block part.
	BlockPartSizeBytes uint32 = 65536 // 64kB

	// MaxBlockPartsCount is the maximum number of block parts.
	MaxBlockPartsCount = (MaxBlockSizeBytes / BlockPartSizeBytes) + 1

	// Restrict the upper bound of the amount of evidence (uses uint16 for safe conversion)
	MaxEvidencePerBlock = 65535
)

// DefaultConsensusParams returns a default ConsensusParams.
func DefaultConsensusParams() *tmproto.ConsensusParams {
	return &tmproto.ConsensusParams{
		Block:     DefaultBlockParams(),
		ChainLock: DefaultChainLockParams(),
		Evidence:  DefaultEvidenceParams(),
		Validator: DefaultValidatorParams(),
		Version:   DefaultVersionParams(),
	}
}

// DefaultBlockParams returns a default BlockParams.
func DefaultBlockParams() tmproto.BlockParams {
	return tmproto.BlockParams{
		MaxBytes:   22020096, // 21MB
		MaxGas:     -1,
		TimeIotaMs: 1000, // 1s
	}
}

// DefaultBlockParams returns a default BlockParams.
func DefaultChainLockParams() tmproto.ChainLockParams {
	chainLockParam := tmproto.ChainLockParam{
		ChainLockHeight:   1,
		ChainLockHash:  []uint8{0x72, 0x3e, 0x3, 0x4e, 0x19, 0x53, 0x35, 0x75, 0xe7,
			0x0, 0x1d, 0xfe, 0x14, 0x34, 0x17, 0xcf, 0x61, 0x72, 0xf3, 0xf3, 0xd6,
			0x5, 0x8d, 0xdd, 0x60, 0xf7, 0x20, 0x99, 0x3, 0x28, 0xce, 0xde},
		Signature: []uint8{0xa3, 0x10, 0xf8, 0x2c, 0x3, 0xf7, 0x39, 0xa0, 0x1, 0x84,
			0x91, 0x77, 0x4d, 0xe1, 0x87, 0xd9, 0x9a, 0x12, 0x11, 0x4d, 0xee, 0xb5,
			0x5e, 0x4, 0x3c, 0x10, 0xd2, 0x91, 0x4e, 0xe5, 0x7e, 0xb8, 0xff, 0x10,
			0x82, 0xef, 0x89, 0x8e, 0x56, 0x5f, 0xbc, 0x24, 0xdc, 0x5f, 0xe2, 0x3,
			0xce, 0x75, 0x2d, 0xdc, 0x50, 0x1b, 0x1d, 0x34, 0xe1, 0xad, 0x7a, 0x82,
			0x1a, 0x8d, 0x7d, 0xe9, 0x6c, 0xcb, 0xe, 0xe3, 0x82, 0x80, 0xe4, 0xe3,
			0xb5, 0x48, 0x8d, 0x1f, 0x59, 0x10, 0xfc, 0x4d, 0xd8, 0x97, 0x58, 0x38,
			0x97, 0xd0, 0x91, 0xc1, 0x50, 0x4d, 0x69, 0x4a, 0xdd, 0x77, 0xd8, 0x88,
			0x2b, 0xdf},
	}
	return tmproto.ChainLockParams{
		NewestChainLock: chainLockParam,
		MinAcceptableChainLockedHeight: 1,
		MinDesiredChainLockedHeight: 1,
	}
}

// DefaultEvidenceParams returns a default EvidenceParams.
func DefaultEvidenceParams() tmproto.EvidenceParams {
	return tmproto.EvidenceParams{
		MaxAgeNumBlocks:  100000, // 27.8 hrs at 1block/s
		MaxAgeDuration:   48 * time.Hour,
		MaxNum:           50,
		ProofTrialPeriod: 50000, // half MaxAgeNumBlocks
	}
}

// DefaultValidatorParams returns a default ValidatorParams, which allows
// only bls12381 pubkeys.
func DefaultValidatorParams() tmproto.ValidatorParams {
	return tmproto.ValidatorParams{
		PubKeyTypes: []string{ABCIPubKeyTypeBLS12381},
	}
}

func DefaultVersionParams() tmproto.VersionParams {
	return tmproto.VersionParams{
		AppVersion: 0,
	}
}

func IsValidPubkeyType(params tmproto.ValidatorParams, pubkeyType string) bool {
	for i := 0; i < len(params.PubKeyTypes); i++ {
		if params.PubKeyTypes[i] == pubkeyType {
			return true
		}
	}
	return false
}

// Validate validates the ConsensusParams to ensure all values are within their
// allowed limits, and returns an error if they are not.
func ValidateConsensusParams(params tmproto.ConsensusParams) error {
	if params.Block.MaxBytes <= 0 {
		return fmt.Errorf("block.MaxBytes must be greater than 0. Got %d",
			params.Block.MaxBytes)
	}
	if params.Block.MaxBytes > MaxBlockSizeBytes {
		return fmt.Errorf("block.MaxBytes is too big. %d > %d",
			params.Block.MaxBytes, MaxBlockSizeBytes)
	}

	if params.Block.MaxGas < -1 {
		return fmt.Errorf("block.MaxGas must be greater or equal to -1. Got %d",
			params.Block.MaxGas)
	}

	if params.Block.TimeIotaMs <= 0 {
		return fmt.Errorf("block.TimeIotaMs must be greater than 0. Got %v",
			params.Block.TimeIotaMs)
	}

	if params.Evidence.MaxAgeNumBlocks <= 0 {
		return fmt.Errorf("evidenceParams.MaxAgeNumBlocks must be greater than 0. Got %d",
			params.Evidence.MaxAgeNumBlocks)
	}

	if params.Evidence.MaxAgeDuration <= 0 {
		return fmt.Errorf("evidenceParams.MaxAgeDuration must be grater than 0 if provided, Got %v",
			params.Evidence.MaxAgeDuration)
	}

	if params.Evidence.MaxNum > MaxEvidencePerBlock {
		return fmt.Errorf("evidenceParams.MaxNumEvidence is greater than upper bound, %d > %d",
			params.Evidence.MaxNum, MaxEvidencePerBlock)
	}

	if int64(params.Evidence.MaxNum)*MaxEvidenceBytesForKeyType(crypto.BLS12381) > params.Block.MaxBytes {
		return fmt.Errorf("total possible evidence size is bigger than block.MaxBytes, %d > %d",
			int64(params.Evidence.MaxNum)*MaxEvidenceBytesForKeyType(crypto.BLS12381), params.Block.MaxBytes)
	}

	if params.Evidence.ProofTrialPeriod <= 0 {
		return fmt.Errorf("evidenceParams.ProofTrialPeriod must be grater than 0 if provided, Got %v",
			params.Evidence.ProofTrialPeriod)
	}

	if params.Evidence.ProofTrialPeriod >= params.Evidence.MaxAgeNumBlocks {
		return fmt.Errorf("evidenceParams.ProofTrialPeriod must be smaller than evidenceParams.MaxAgeNumBlocks,  %d > %d",
			params.Evidence.ProofTrialPeriod, params.Evidence.MaxAgeDuration)
	}

	if len(params.Validator.PubKeyTypes) == 0 {
		return errors.New("len(Validator.PubKeyTypes) must be greater than 0")
	}

	// Check if keyType is a known ABCIPubKeyType
	for i := 0; i < len(params.Validator.PubKeyTypes); i++ {
		keyType := params.Validator.PubKeyTypes[i]
		if _, ok := ABCIPubKeyTypesToNames[keyType]; !ok {
			return fmt.Errorf("params.Validator.PubKeyTypes[%d], %s, is an unknown pubkey type",
				i, keyType)
		}
	}

	return nil
}

// Hash returns a hash of a subset of the parameters to store in the block header.
// Only the Block.MaxBytes and Block.MaxGas are included in the hash.
// This allows the ConsensusParams to evolve more without breaking the block
// protocol. No need for a Merkle tree here, just a small struct to hash.
func HashConsensusParams(params tmproto.ConsensusParams) []byte {
	hasher := tmhash.New()

	hp := tmproto.HashedParams{
		BlockMaxBytes: params.Block.MaxBytes,
		BlockMaxGas:   params.Block.MaxGas,
	}

	bz, err := hp.Marshal()
	if err != nil {
		panic(err)
	}

	hasher.Write(bz)
	return hasher.Sum(nil)
}

// Update returns a copy of the params with updates from the non-zero fields of p2.
// NOTE: note: must not modify the original
func UpdateConsensusParams(params tmproto.ConsensusParams, params2 *abci.ConsensusParams) tmproto.ConsensusParams {
	res := params // explicit copy

	if params2 == nil {
		return res
	}

	// we must defensively consider any structs may be nil
	if params2.Block != nil {
		res.Block.MaxBytes = params2.Block.MaxBytes
		res.Block.MaxGas = params2.Block.MaxGas
	}
	if params2.Evidence != nil {
		res.Evidence.MaxAgeNumBlocks = params2.Evidence.MaxAgeNumBlocks
		res.Evidence.MaxAgeDuration = params2.Evidence.MaxAgeDuration
		res.Evidence.MaxNum = params2.Evidence.MaxNum
	}
	if params2.Validator != nil {
		// Copy params2.Validator.PubkeyTypes, and set result's value to the copy.
		// This avoids having to initialize the slice to 0 values, and then write to it again.
		res.Validator.PubKeyTypes = append([]string{}, params2.Validator.PubKeyTypes...)
	}
	if params2.Version != nil {
		res.Version.AppVersion = params2.Version.AppVersion
	}
	return res
}
