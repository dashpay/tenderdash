package light_test

import (
	"time"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/mock"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/crypto/tmhash"
	provider_mocks "github.com/tendermint/tendermint/light/provider/mocks"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
	"github.com/tendermint/tendermint/version"
)

// privKeys is a helper type for testing.
//
// It lets us simulate signing with many keys.  The main use case is to create
// a set, and call GenSignedHeader to get properly signed header for testing.
//
// You can set different weights of validators each time you call ToValidators,
// and can optionally extend the validator set later with Extend.
type privKeys []crypto.PrivKey

func exposeMockPVKeys(pvs []types.PrivValidator, quorumHash crypto.QuorumHash) privKeys {
	res := make(privKeys, len(pvs))
	for i, pval := range pvs {
		mockPV := pval.(*types.MockPV)
		res[i] = mockPV.PrivateKeys[quorumHash.String()].PrivKey
	}
	return res
}

// ToValidators produces a valset from the set of keys.
// The first key has weight `init` and it increases by `inc` every step
// so we can have all the same weight, or a simple linear distribution
// (should be enough for testing).
func (pkz privKeys) ToValidators(thresholdPublicKey crypto.PubKey) *types.ValidatorSet {
	res := make([]*types.Validator, len(pkz))
	for i, k := range pkz {
		res[i] = types.NewValidatorDefaultVotingPower(k.PubKey(), crypto.Sha256(k.PubKey().Address()))
	}
	// Quorum hash is pseudorandom
	return types.NewValidatorSet(
		res,
		thresholdPublicKey,
		btcjson.LLMQType_5_60,
		crypto.Sha256(thresholdPublicKey.Bytes()),
		true,
	)
}

// signHeader properly signs the header with all keys from first to last exclusive.
func (pkz privKeys) signHeader(header *types.Header, valSet *types.ValidatorSet, first, last int) *types.Commit {
	var blockSigs [][]byte
	var stateSigs [][]byte
	var blsIDs [][]byte

	blockID := types.BlockID{
		Hash:          header.Hash(),
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: crypto.CRandBytes(32)},
	}

	stateID := types.StateID{
		Height:      header.Height - 1,
		LastAppHash: header.AppHash,
	}

	// Fill in the votes we want.
	for i := first; i < last && i < len(pkz); i++ {
		// Verify that the private key matches the validator proTxHash
		privateKey := pkz[i]
		proTxHash, val := valSet.GetByIndex(int32(i))
		if val == nil {
			panic("no val")
		}
		if privateKey == nil {
			panic("no priv key")
		}
		if !privateKey.PubKey().Equals(val.PubKey) {
			panic("light client keys do not match")
		}
		vote := makeVote(header, valSet, proTxHash, pkz[i], blockID, stateID)
		blockSigs = append(blockSigs, vote.BlockSignature)
		stateSigs = append(stateSigs, vote.StateSignature)
		blsIDs = append(blsIDs, vote.ValidatorProTxHash)
	}

	thresholdBlockSig, _ := bls12381.RecoverThresholdSignatureFromShares(blockSigs, blsIDs)
	thresholdStateSig, _ := bls12381.RecoverThresholdSignatureFromShares(stateSigs, blsIDs)

	return types.NewCommit(header.Height, 1, blockID, stateID, valSet.QuorumHash, thresholdBlockSig, thresholdStateSig)
}

func makeVote(header *types.Header, valset *types.ValidatorSet, proTxHash crypto.ProTxHash,
	key crypto.PrivKey, blockID types.BlockID, stateID types.StateID) *types.Vote {

	idx, val := valset.GetByProTxHash(proTxHash)
	if val == nil {
		panic("val must exist")
	}
	vote := &types.Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     idx,
		Height:             header.Height,
		Round:              1,
		Type:               tmproto.PrecommitType,
		BlockID:            blockID,
	}

	v := vote.ToProto()
	// SignDigest the vote
	signID := types.VoteBlockSignID(header.ChainID, v, valset.QuorumType, valset.QuorumHash)
	sig, err := key.SignDigest(signID)
	if err != nil {
		panic(err)
	}

	// SignDigest the state
	stateSignID := stateID.SignID(header.ChainID, valset.QuorumType, valset.QuorumHash)
	sigState, err := key.SignDigest(stateSignID)
	if err != nil {
		panic(err)
	}

	vote.BlockSignature = sig
	vote.StateSignature = sigState

	return vote
}

func genHeader(chainID string, height int64, bTime time.Time, txs types.Txs,
	valset, nextValset *types.ValidatorSet, appHash, consHash, resHash []byte) *types.Header {

	return &types.Header{
		Version: version.Consensus{Block: version.BlockProtocol, App: 0},
		ChainID: chainID,
		Height:  height,
		Time:    bTime,
		// LastBlockID
		// LastCommitHash
		ValidatorsHash:     valset.Hash(),
		NextValidatorsHash: nextValset.Hash(),
		DataHash:           txs.Hash(),
		AppHash:            appHash,
		ConsensusHash:      consHash,
		LastResultsHash:    resHash,
		ProposerProTxHash:  valset.Validators[0].ProTxHash,
	}
}

// GenSignedHeader calls genHeader and signHeader and combines them into a SignedHeader.
func (pkz privKeys) GenSignedHeader(chainID string, height int64, bTime time.Time, txs types.Txs,
	valset, nextValset *types.ValidatorSet, appHash, consHash, resHash []byte, first, last int) *types.SignedHeader {

	header := genHeader(chainID, height, bTime, txs, valset, nextValset, appHash, consHash, resHash)
	return &types.SignedHeader{
		Header: header,
		Commit: pkz.signHeader(header, valset, first, last),
	}
}

// GenSignedHeaderLastBlockID calls genHeader and signHeader and combines them into a SignedHeader.
func (pkz privKeys) GenSignedHeaderLastBlockID(chainID string, height int64, bTime time.Time, txs types.Txs,
	valset, nextValset *types.ValidatorSet, appHash, consHash, resHash []byte, first, last int,
	lastBlockID types.BlockID) *types.SignedHeader {

	header := genHeader(chainID, height, bTime, txs, valset, nextValset, appHash, consHash, resHash)
	header.LastBlockID = lastBlockID
	return &types.SignedHeader{
		Header: header,
		Commit: pkz.signHeader(header, valset, first, last),
	}
}

func hash(s string) []byte {
	return tmhash.Sum([]byte(s))
}

func mockNodeFromHeadersAndVals(
	headers map[int64]*types.SignedHeader,
	vals map[int64]*types.ValidatorSet,
) *provider_mocks.Provider {
	provider := &provider_mocks.Provider{}
	for i, header := range headers {
		lb := &types.LightBlock{SignedHeader: header, ValidatorSet: vals[i]}
		provider.
			On("LightBlock", mock.Anything, i).
			Return(lb, nil)
	}
	return provider
}

// genLightBlocksWithValidatorsRotatingEveryBlock generates the header and validator set to create
// blocks to height. BlockIntervals are in per minute.
// NOTE: Expected to have a large validator set size ~ 100 validators.
func genLightBlocksWithValidatorsRotatingEveryBlock(
	chainID string,
	numBlocks int64,
	valSize int,
	bTime time.Time) (
	map[int64]*types.SignedHeader,
	map[int64]*types.ValidatorSet,
	[]types.PrivValidator) {

	var (
		headers = make(map[int64]*types.SignedHeader, numBlocks)
		valset  = make(map[int64]*types.ValidatorSet, numBlocks+1)
	)

	vals, privVals := types.RandValidatorSet(valSize)
	keys := exposeMockPVKeys(privVals, vals.QuorumHash)

	newVals, newPrivVals := types.GenerateValidatorSet(
		types.NewValSetParam(vals.GetProTxHashes()),
		types.WithUpdatePrivValAt(privVals, 1),
	)
	newKeys := exposeMockPVKeys(newPrivVals, newVals.QuorumHash)

	// genesis header and vals
	lastHeader := keys.GenSignedHeader(chainID, 1, bTime.Add(1*time.Minute), nil,
		vals, newVals, hash("app_hash"), hash("cons_hash"),
		hash("results_hash"), 0, len(keys))
	currentHeader := lastHeader
	headers[1] = currentHeader
	valset[1] = vals
	keys = newKeys

	for height := int64(2); height <= numBlocks; height++ {
		newVals, newPrivVals := types.GenerateValidatorSet(
			types.NewValSetParam(vals.GetProTxHashes()),
			types.WithUpdatePrivValAt(privVals, height),
		)
		newKeys = exposeMockPVKeys(newPrivVals, newVals.QuorumHash)

		currentHeader = keys.GenSignedHeaderLastBlockID(chainID, height, bTime.Add(time.Duration(height)*time.Minute),
			nil,
			vals, newVals, hash("app_hash"), hash("cons_hash"),
			hash("results_hash"), 0, len(keys), types.BlockID{Hash: lastHeader.Hash()})
		headers[height] = currentHeader
		valset[height] = vals
		lastHeader = currentHeader
		keys = newKeys
	}

	return headers, valset, newPrivVals
}
