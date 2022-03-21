package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/tmhash"
	"github.com/tendermint/tendermint/internal/test/factory"
	tmjson "github.com/tendermint/tendermint/libs/json"
	"github.com/tendermint/tendermint/privval"
	e2e "github.com/tendermint/tendermint/test/e2e/pkg"
	"github.com/tendermint/tendermint/types"
)

// InjectEvidence takes a running testnet and generates an amount of valid
// evidence and broadcasts it to a random node through the rpc endpoint `/broadcast_evidence`.
// Evidence is random and can be a mixture of DuplicateVoteEvidence.
func InjectEvidence(ctx context.Context, r *rand.Rand, testnet *e2e.Testnet, amount int) error {
	// select a random node
	var targetNode *e2e.Node

	for _, idx := range r.Perm(len(testnet.Nodes)) {
		targetNode = testnet.Nodes[idx]

		if targetNode.Mode == e2e.ModeSeed || targetNode.Mode == e2e.ModeLight {
			targetNode = nil
			continue
		}

		break
	}

	if targetNode == nil {
		return errors.New("could not find node to inject evidence into")
	}

	logger.Info(fmt.Sprintf("Injecting evidence through %v (amount: %d)...", targetNode.Name, amount))

	client, err := targetNode.Client()
	if err != nil {
		return err
	}

	// request the latest block and validator set from the node
	blockRes, err := client.Block(context.Background(), nil)
	if err != nil {
		return err
	}
	evidenceHeight := blockRes.Block.Height
	waitHeight := blockRes.Block.Height + 3

	nValidators := 100
	requestQuorumInfo := true
	valRes, err := client.Validators(context.Background(), &evidenceHeight, nil, &nValidators, &requestQuorumInfo)
	if err != nil {
		return err
	}

	if valRes.ThresholdPublicKey == nil {
		return errors.New("threshold public key must be returned when requested")
	}

	if valRes.QuorumHash == nil {
		return errors.New("quorum hash must be returned when requested")
	}

	valSet, err := types.ValidatorSetFromExistingValidators(valRes.Validators, *valRes.ThresholdPublicKey, valRes.QuorumType, *valRes.QuorumHash)
	if err != nil {
		return err
	}

	// get the private keys of all the validators in the network
	privVals, err := getPrivateValidatorKeys(testnet, *valRes.ThresholdPublicKey, *valRes.QuorumHash)
	if err != nil {
		return err
	}

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	// wait for the node to reach the height above the forged height so that
	// it is able to validate the evidence
	_, err = waitForNode(wctx, targetNode, waitHeight)
	if err != nil {
		return err
	}

	var ev types.Evidence
	for i := 1; i <= amount; i++ {
		ev, err = generateDuplicateVoteEvidence(
			privVals, evidenceHeight, valSet, testnet.Name, blockRes.Block.Time,
		)
		if err != nil {
			return err
		}

		_, err := client.BroadcastEvidence(context.Background(), ev)
		if err != nil {
			return err
		}
	}

	wctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// wait for the node to reach the height above the forged height so that
	// it is able to validate the evidence
	_, err = waitForNode(wctx, targetNode, blockRes.Block.Height+2)
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("Finished sending evidence (height %d)", blockRes.Block.Height+2))

	return nil
}

func getPrivateValidatorKeys(testnet *e2e.Testnet, thresholdPublicKey crypto.PubKey, quorumHash crypto.QuorumHash) ([]*types.MockPV, error) {
	var privVals []*types.MockPV

	for _, node := range testnet.Nodes {
		if node.Mode == e2e.ModeValidator {
			privKeyPath := filepath.Join(testnet.Dir, node.Name, PrivvalKeyFile)
			privKey, err := readPrivKey(privKeyPath, quorumHash)
			if err != nil {
				return nil, err
			}
			// Create mock private validators from the validators private key. MockPV is
			// stateless which means we can double vote and do other funky stuff
			privVals = append(
				privVals,
				types.NewMockPVWithParams(
					privKey,
					node.ProTxHash,
					quorumHash,
					thresholdPublicKey,
					false,
					false,
				),
			)
		}
	}

	return privVals, nil
}

// generateDuplicateVoteEvidence picks a random validator from the val set and
// returns duplicate vote evidence against the validator
func generateDuplicateVoteEvidence(
	privVals []*types.MockPV,
	height int64,
	vals *types.ValidatorSet,
	chainID string,
	time time.Time,
) (*types.DuplicateVoteEvidence, error) {
	privVal, valIdx, err := getRandomValidatorIndex(privVals, vals)
	if err != nil {
		return nil, err
	}
	stateID := types.RandStateID()
	voteA, err := factory.MakeVote(privVal, vals, chainID, valIdx, height, 0, 2, makeRandomBlockID(), stateID)
	if err != nil {
		return nil, err
	}
	voteB, err := factory.MakeVote(privVal, vals, chainID, valIdx, height, 0, 2, makeRandomBlockID(), stateID)
	if err != nil {
		return nil, err
	}
	ev, err := types.NewDuplicateVoteEvidence(voteA, voteB, time, vals)
	if err != nil {
		return nil, fmt.Errorf("could not generate evidence: %w", err)
	}

	return ev, nil
}

// getRandomValidatorIndex picks a random validator from a slice of mock PrivVals that's
// also part of the validator set, returning the PrivVal and its index in the validator set
func getRandomValidatorIndex(privVals []*types.MockPV, vals *types.ValidatorSet) (*types.MockPV, int32, error) {
	for _, idx := range rand.Perm(len(privVals)) {
		pv := privVals[idx]
		valIdx, _ := vals.GetByProTxHash(pv.ProTxHash)
		if valIdx >= 0 {
			return pv, valIdx, nil
		}
	}
	return nil, -1, errors.New("no private validator found in validator set")
}

func readPrivKey(keyFilePath string, quorumHash crypto.QuorumHash) (crypto.PrivKey, error) {
	keyJSONBytes, err := ioutil.ReadFile(keyFilePath)
	if err != nil {
		return nil, err
	}
	pvKey := privval.FilePVKey{}
	err = tmjson.Unmarshal(keyJSONBytes, &pvKey)
	if err != nil {
		return nil, fmt.Errorf("error reading PrivValidator key from %v: %w", keyFilePath, err)
	}
	return pvKey.PrivateKeys[quorumHash.String()].PrivKey, nil
}

func makeRandomBlockID() types.BlockID {
	return makeBlockID(crypto.CRandBytes(tmhash.Size), 100, crypto.CRandBytes(tmhash.Size))
}

func makeBlockID(hash []byte, partSetSize uint32, partSetHash []byte) types.BlockID {
	var (
		h   = make([]byte, tmhash.Size)
		psH = make([]byte, tmhash.Size)
	)
	copy(h, hash)
	copy(psH, partSetHash)
	return types.BlockID{
		Hash: h,
		PartSetHeader: types.PartSetHeader{
			Total: partSetSize,
			Hash:  psH,
		},
	}
}
