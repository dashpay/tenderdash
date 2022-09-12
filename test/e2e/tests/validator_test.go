package e2e_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	cryptoenc "github.com/tendermint/tendermint/crypto/encoding"
	e2e "github.com/tendermint/tendermint/test/e2e/pkg"
	"github.com/tendermint/tendermint/types"
)

// Tests that validator sets are available and correct according to
// scheduled validator updates.
func TestValidator_Sets(t *testing.T) {
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
		client, err := node.Client()
		require.NoError(t, err)
		status, err := client.Status(ctx)
		require.NoError(t, err)

		first := status.SyncInfo.EarliestBlockHeight

		// for nodes that have to catch up, we should only
		// check the validator sets for nodes after this
		// point, to avoid inconsistencies with backfill.
		if node.StartAt > first {
			first = node.StartAt
		}

		last := status.SyncInfo.LatestBlockHeight

		// skip first block if node is pruning blocks, to avoid race conditions
		if node.RetainBlocks > 0 {
			first++
		}

		valSchedule := newValidatorSchedule(*node.Testnet)
		require.NoError(t, valSchedule.Increment(first-node.Testnet.InitialHeight))

		for h := first; h <= last; h++ {
			validators := []*types.Validator{}
			var thresholdPublicKey crypto.PubKey
			perPage := 100
			for page := 1; ; page++ {
				requestQuorumInfo := page == 1
				resp, err := client.Validators(ctx, &(h), &(page), &perPage, &requestQuorumInfo)
				require.NoError(t, err)
				validators = append(validators, resp.Validators...)
				if requestQuorumInfo {
					thresholdPublicKey = *resp.ThresholdPublicKey
				}
				if len(validators) == resp.Total {
					break
				}
			}
			// fmt.Printf("node %s(%X) validator set for height %d is %v\n",
			//	node.Name, node.ProTxHash, h, valSchedule.Set)
			for i, valScheduleValidator := range valSchedule.Set.Validators {
				validator := validators[i]
				require.Equal(t, valScheduleValidator.ProTxHash, validator.ProTxHash,
					"mismatching validator proTxHashes at height %v (%X <=> %X", h,
					valScheduleValidator.ProTxHash, validator.ProTxHash)
				require.Equal(t, valScheduleValidator.PubKey.Bytes(), validator.PubKey.Bytes(),
					"mismatching validator %X publicKey at height %v (%X <=> %X",
					valScheduleValidator.ProTxHash, h, valScheduleValidator.PubKey.Bytes(), validator.PubKey.Bytes())

				// Validators in the schedule don't contain addresses
				validator.NodeAddress = types.ValidatorAddress{}
			}
			require.Equal(t, valSchedule.Set.Validators, validators,
				"incorrect validator set at height %v", h)
			require.Equal(t, valSchedule.Set.ThresholdPublicKey, thresholdPublicKey,
				"incorrect thresholdPublicKey at height %v", h)
			require.NoError(t, valSchedule.Increment(1))
		}
	})
}

// Tests that a validator proposes blocks when it's supposed to. It tolerates some
// missed blocks, e.g. due to testnet perturbations.
func TestValidator_Propose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocks := fetchBlockChain(ctx, t)
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
		if node.Mode != e2e.ModeValidator {
			return
		}
		proTxHash := node.ProTxHash
		valSchedule := newValidatorSchedule(*node.Testnet)

		expectCount := 0
		proposeCount := 0
		for _, block := range blocks {
			if bytes.Equal(valSchedule.Set.Proposer.ProTxHash, proTxHash) {
				expectCount++
				if bytes.Equal(block.ProposerProTxHash, proTxHash) {
					proposeCount++
				}
			}
			require.NoError(t, valSchedule.Increment(1))
		}

		require.False(t, proposeCount == 0 && expectCount > 0,
			"node did not propose any blocks (expected %v)", expectCount)
		if expectCount > 5 {
			require.GreaterOrEqual(t, proposeCount, 3, "validator didn't propose even 3 blocks")
		}
	})
}

// validatorSchedule is a validator set iterator, which takes into account
// validator set updates.
type validatorSchedule struct {
	Set                       *types.ValidatorSet
	height                    int64
	updates                   map[int64]e2e.ValidatorsMap
	thresholdPublicKeyUpdates map[int64]crypto.PubKey
	quorumHashUpdates         map[int64]crypto.QuorumHash
}

func newValidatorSchedule(testnet e2e.Testnet) *validatorSchedule {
	valMap := testnet.Validators // genesis validators
	thresholdPublicKey := testnet.ThresholdPublicKey
	quorumHash := testnet.QuorumHash
	quorumType := btcjson.LLMQType_5_60
	if thresholdPublicKey == nil {
		panic("threshold public key must be set")
	}
	if v, ok := testnet.ValidatorUpdates[0]; ok { // InitChain validators
		valMap = v
		thresholdPublicKey, ok = testnet.ThresholdPublicKeyUpdates[0]
		if !ok { // InitChain threshold public key
			panic("threshold public key must be set for height 0 if validator changes")
		}
		quorumHash, ok = testnet.QuorumHashUpdates[0]
		if !ok { // InitChain threshold public key
			panic("quorum hash key must be set for height 0 if validator changes")
		}
	}

	return &validatorSchedule{
		height:                    testnet.InitialHeight,
		Set:                       types.NewValidatorSet(makeVals(valMap), thresholdPublicKey, quorumType, quorumHash, true),
		updates:                   testnet.ValidatorUpdates,
		thresholdPublicKeyUpdates: testnet.ThresholdPublicKeyUpdates,
		quorumHashUpdates:         testnet.QuorumHashUpdates,
	}
}

func (s *validatorSchedule) Increment(heights int64) error {
	for i := int64(0); i < heights; i++ {
		s.height++
		if s.height > 2 {
			// validator set updates are offset by 2, since they only take effect
			// two blocks after they're returned.
			if update, ok := s.updates[s.height-2]; ok {
				if thresholdPublicKeyUpdate, ok := s.thresholdPublicKeyUpdates[s.height-2]; ok {
					if quorumHashUpdate, ok := s.quorumHashUpdates[s.height-2]; ok {
						if bytes.Equal(quorumHashUpdate, s.Set.QuorumHash) {
							if err := s.Set.UpdateWithChangeSet(makeVals(update), thresholdPublicKeyUpdate, quorumHashUpdate); err != nil {
								return err
							}
						} else {
							s.Set = types.NewValidatorSet(makeVals(update), thresholdPublicKeyUpdate, btcjson.LLMQType_5_60,
								quorumHashUpdate, true)
						}
					}
				}
			}
		}
		s.Set.IncrementProposerPriority(1)
	}
	return nil
}

func makeVals(valMap e2e.ValidatorsMap) []*types.Validator {
	vals := make([]*types.Validator, 0, len(valMap))
	for node, valUpdate := range valMap {
		pubkey := valUpdate.PubKey
		pk, err := cryptoenc.PubKeyFromProto(*pubkey)
		if err != nil {
			panic(err)
		}
		vals = append(vals, types.NewValidatorDefaultVotingPower(pk, node.ProTxHash))
	}
	return vals
}
