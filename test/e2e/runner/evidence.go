package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/types"
)

// InjectEvidence takes a running testnet and generates an amount of valid
// evidence and broadcasts it to a random node through the rpc endpoint `/broadcast_evidence`.
// Evidence is random and can be a mixture of DuplicateVoteEvidence.
func InjectEvidence(ctx context.Context, logger log.Logger, r *rand.Rand, testnet *e2e.Testnet, amount int) error {
	// select a random node
	var targetNode *e2e.Node

	for _, idx := range r.Perm(len(testnet.Nodes)) {
		if !testnet.Nodes[idx].Stateless() {
			targetNode = testnet.Nodes[idx]
			break
		}
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
	blockRes, err := client.Block(ctx, nil)
	if err != nil {
		return err
	}
	evidenceHeight := blockRes.Block.Height - 3

	nValidators := 100
	requestQuorumInfo := true
	valRes, err := client.Validators(ctx, &evidenceHeight, nil, &nValidators, &requestQuorumInfo)
	if err != nil {
		return err
	}

	if valRes.ThresholdPublicKey == nil {
		return errors.New("threshold public key must be returned when requested")
	}

	if valRes.QuorumHash == nil {
		return errors.New("quorum hash must be returned when requested")
	}

	valSet := types.NewValidatorSet(valRes.Validators, *valRes.ThresholdPublicKey,
		valRes.QuorumType,
		*valRes.QuorumHash,
		false)
	if valSet == nil {
		return fmt.Errorf("could not create validator set from response")
	}
	if err = valSet.SetProposer(blockRes.Block.ProposerProTxHash); err != nil {
		return fmt.Errorf("could not set proposer: %w", err)
	}

	// get the private keys of all the validators in the network
	privVals, err := getPrivateValidatorKeys(testnet, *valRes.ThresholdPublicKey, *valRes.QuorumHash)
	if err != nil {
		return err
	}

	// request the latest block and validator set from the node
	blockRes, err = client.Block(ctx, &evidenceHeight)
	if err != nil {
		return err
	}

	var ev types.Evidence
	for i := 1; i <= amount; i++ {
		ev, err = generateDuplicateVoteEvidence(ctx,
			privVals, evidenceHeight, valSet, testnet.Name, blockRes.Block.Time,
		)
		if err != nil {
			return err
		}

		_, err := client.BroadcastEvidence(ctx, ev)
		if err != nil {
			return err
		}
	}

	logger.Info("Finished sending evidence",
		"node", testnet.Name,
		"amount", amount,
		"height", evidenceHeight,
	)

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	// wait for the node to make progress after submitting
	// evidence (3 (forged height) + 1 (progress))
	_, err = waitForNode(wctx, logger, targetNode, evidenceHeight+4)
	if err != nil {
		return err
	}

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
	ctx context.Context,
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
	voteA, err := factory.MakeVote(ctx, privVal, vals, chainID, valIdx, height, 0, 2, makeRandomBlockID())
	if err != nil {
		return nil, err
	}
	voteB, err := factory.MakeVote(ctx, privVal, vals, chainID, valIdx, height, 0, 2, makeRandomBlockID())
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
	keyJSONBytes, err := os.ReadFile(keyFilePath)
	if err != nil {
		return nil, err
	}
	pvKey := privval.FilePVKey{}
	err = json.Unmarshal(keyJSONBytes, &pvKey)
	if err != nil {
		return nil, fmt.Errorf("error reading PrivValidator key from %v: %w", keyFilePath, err)
	}
	return pvKey.PrivateKeys[quorumHash.String()].PrivKey, nil
}

func makeRandomBlockID() types.BlockID {
	return makeBlockID(tmrand.Bytes(crypto.HashSize), 100, tmrand.Bytes(crypto.HashSize), types.RandStateID())
}

func makeBlockID(hash tmbytes.HexBytes, partSetSize uint32, partSetHash tmbytes.HexBytes, stateID tmproto.StateID) types.BlockID {
	return types.BlockID{
		Hash: hash.Copy(),
		PartSetHeader: types.PartSetHeader{
			Total: partSetSize,
			Hash:  partSetHash.Copy(),
		},
		StateID: stateID.Hash(),
	}
}
