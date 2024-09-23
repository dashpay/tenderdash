package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	cryptoenc "github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/types"
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
		// limit the test to 100 blocks, to avoid long test times
		if last > first+100 {
			last = first + 100
		}

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
			for i, valScheduleValidator := range valSchedule.ValidatorProposer.ValidatorSet().Validators {
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
			require.Equal(t, valSchedule.ValidatorProposer.ValidatorSet().Validators, validators,
				"incorrect validator set at height %v", h)
			require.Equal(t, valSchedule.ValidatorProposer.ValidatorSet().ThresholdPublicKey, thresholdPublicKey,
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

	// check if proposer order is correct, that it:
	// - validators don't vote twice in a row
	// - validators vote in ascending proTxHash order
	// - validators vote in round-robin order
	var prevProposer tmbytes.HexBytes
	var prevBlock *types.Block
	for _, block := range blocks {
		currentProposer := block.ProposerProTxHash
		require.NotEmpty(t, currentProposer, "block %v has no proposer", block.Height)

		// don't verify heights where validator rotation happens
		if prevBlock != nil && prevBlock.ValidatorsHash.Equal(block.ValidatorsHash) {
			assert.NotEqual(t, prevProposer, currentProposer,
				"validator %s proposed two blocks in a row", currentProposer.ShortString())
			assert.Less(t, prevProposer, currentProposer,
				"previous proposer %s is higher than proposer %s at height %d",
				prevProposer.ShortString(), currentProposer.ShortString(), block.Header.Height)
		}

		prevBlock = block
	}

	testNode(t, func(_ctx context.Context, t *testing.T, node e2e.Node) {
		if node.Mode != e2e.ModeValidator {
			return
		}
		proTxHash := node.ProTxHash
		valSchedule := newValidatorSchedule(*node.Testnet)

		expectCount := 0
		proposeCount := 0
		for i, block := range blocks {
			round := int32(0)
			if i+1 < len(blocks) { // we might be missing the last commit, we'll assume round 0
				round = blocks[i+1].LastCommit.Round
			}
			expectedProposer := valSchedule.ValidatorProposer.MustGetProposer(block.Height, round).ProTxHash
			if bytes.Equal(expectedProposer, proTxHash) {
				expectCount++
				if bytes.Equal(block.ProposerProTxHash, proTxHash) {
					proposeCount++
				}
			}
			require.NoError(t, valSchedule.Increment(1))
		}

		require.False(t, proposeCount == 0 && expectCount > 0,
			"node with proTxHash %s did not propose any blocks (expected %v)",
			proTxHash.ShortString(),
			expectCount,
		)
		if expectCount > 5 {
			require.GreaterOrEqual(t, proposeCount, 3, "validator didn't propose even 3 blocks")
		}
	})
}

// validatorSchedule is a validator set iterator, which takes into account
// validator set updates.
type validatorSchedule struct {
	ValidatorProposer         selectproposer.ProposerSelector
	height                    int64
	updates                   map[int64]e2e.ValidatorsMap
	thresholdPublicKeyUpdates map[int64]crypto.PubKey
	quorumHashUpdates         map[int64]crypto.QuorumHash
	consensusVersionUpdates   map[int64]int32

	consensusVersions map[int64]int32
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

	vs := types.NewValidatorSet(makeVals(valMap), thresholdPublicKey, quorumType, quorumHash, true)
	ps, err := selectproposer.NewProposerSelector(*types.DefaultConsensusParams(), vs, testnet.InitialHeight, 0, nil, nil)
	if err != nil {
		panic(err)
	}

	return &validatorSchedule{
		height:                    testnet.InitialHeight,
		ValidatorProposer:         ps,
		updates:                   testnet.ValidatorUpdates,
		thresholdPublicKeyUpdates: testnet.ThresholdPublicKeyUpdates,
		quorumHashUpdates:         testnet.QuorumHashUpdates,
		consensusVersions:         make(map[int64]int32),
		consensusVersionUpdates:   testnet.ConsensusVersionUpdates,
	}
}
func (s *validatorSchedule) consensusVersionUpdate() {
	ver, ok := s.consensusVersionUpdates[s.height]
	if !ok && s.height-1 > 0 {
		ver = s.consensusVersions[s.height-1]
	}

	s.consensusVersions[s.height] = ver
}

func (s *validatorSchedule) ConsensusParams() types.ConsensusParams {
	ver, ok := s.consensusVersions[s.height]
	if !ok {
		panic(fmt.Errorf("consensus version not set for height %d", s.height))
	}

	cp := *types.DefaultConsensusParams()
	cp.Version.ConsensusVersion = ver

	return cp
}

func (s *validatorSchedule) Increment(heights int64) error {
	for i := int64(0); i < heights; i++ {
		s.height++

		// consensus params update - for now, we only support consensus version updates
		s.consensusVersionUpdate()
		cp := s.ConsensusParams()

		// validator set update
		if s.height > 1 {
			// validator set updates are offset by 1, since they only take effect
			// 1 block after they're returned.
			if update, ok := s.updates[s.height-1]; ok {
				if thresholdPublicKeyUpdate, ok := s.thresholdPublicKeyUpdates[s.height-1]; ok {
					if quorumHashUpdate, ok := s.quorumHashUpdates[s.height-1]; ok {
						currentQuorumHash := s.ValidatorProposer.ValidatorSet().QuorumHash
						if bytes.Equal(quorumHashUpdate, currentQuorumHash) {
							vs := s.ValidatorProposer.ValidatorSet()

							if err := vs.UpdateWithChangeSet(makeVals(update), thresholdPublicKeyUpdate, quorumHashUpdate); err != nil {
								return err
							}
						} else {
							vs := types.NewValidatorSet(makeVals(update), thresholdPublicKeyUpdate, btcjson.LLMQType_5_60,
								quorumHashUpdate, true)

							ps, err := selectproposer.NewProposerSelector(cp, vs, s.height, 0, nil, nil)
							if err != nil {
								return err
							}
							if cp.Version.ConsensusVersion == 0 {
								//consensus version 0 had an issue where first proposer didn't propose
								// TODO: double check if this is really needed
								ps.ValidatorSet().IncProposerIndex(1)
							}
							s.ValidatorProposer = ps
						}
					}
				}
			}
		}
		if err := s.ValidatorProposer.UpdateHeightRound(s.height, 0); err != nil {
			return err
		}
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
