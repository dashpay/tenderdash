package types

import (
	// it is ok to use math/rand here: we do not need a cryptographically secure random
	// number generator here and we can run the tests a bit faster
	"context"
	"encoding/hex"
	"fmt"
	"math"
	mrand "math/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	gogotypes "github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/crypto/merkle"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	tmversion "github.com/dashpay/tenderdash/proto/tendermint/version"
	"github.com/dashpay/tenderdash/version"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func TestBlockAddEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	txs := []Tx{Tx("foo"), Tx("bar")}
	lastID := makeBlockIDRandom()

	h := int64(3)

	coreChainLock := NewMockChainLock(1)

	voteSet, valSet, vals := randVoteSet(ctx, t, h-1, 1, tmproto.PrecommitType, 10)
	commit, err := makeCommit(ctx, lastID, h-1, 1, voteSet, vals)
	require.NoError(t, err)

	ev, err := NewMockDuplicateVoteEvidenceWithValidator(ctx, h, time.Now(), vals[0], "block-test-chain", valSet.QuorumType,
		valSet.QuorumHash)
	require.NoError(t, err)
	evList := []Evidence{ev}

	block := MakeBlock(h, txs, commit, evList)
	block.SetCoreChainLock(&coreChainLock)

	require.NotNil(t, block)
	require.Equal(t, 1, len(block.Evidence))
	require.NotNil(t, block.EvidenceHash)
}

func TestBlockValidateBasic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.Error(t, (*Block)(nil).ValidateBasic())

	txs := []Tx{Tx("foo"), Tx("bar")}
	lastID := makeBlockIDRandom()
	h := int64(3)

	voteSet, valSet, vals := randVoteSet(ctx, t, h-1, 1, tmproto.PrecommitType, 10)
	commit, err := makeCommit(ctx, lastID, h-1, 1, voteSet, vals)
	require.NoError(t, err)

	ev, err := NewMockDuplicateVoteEvidenceWithValidator(ctx, h, time.Now(), vals[0], "block-test-chain", valSet.QuorumType,
		valSet.QuorumHash)
	require.NoError(t, err)
	evList := []Evidence{ev}

	testCases := []struct {
		testName      string
		malleateBlock func(*Block)
		expErr        bool
	}{
		{"Make Block", func(blk *Block) {}, false},
		{"Make Block w/ proposer pro_tx_hash", func(blk *Block) {
			blk.ProposerProTxHash = valSet.Proposer().ProTxHash
		}, false},
		{"Negative Height", func(blk *Block) { blk.Height = -1 }, true},
		{"Modify the last Commit", func(blk *Block) {
			blk.LastCommit.BlockID = BlockID{}
			blk.LastCommit.hash = nil // clear hash or change wont be noticed
		}, true},
		{"Remove LastCommitHash", func(blk *Block) {
			blk.LastCommitHash =
				[]byte("something else")
		}, true},
		{"Tampered Data", func(blk *Block) {
			blk.Data.Txs[0] = Tx("something else")
			blk.Data.hash = nil // clear hash or change wont be noticed
		}, true},
		{"Tampered DataHash", func(blk *Block) {
			blk.DataHash = tmrand.Bytes(len(blk.DataHash))
		}, true},
		{"Tampered EvidenceHash", func(blk *Block) {
			blk.EvidenceHash = tmrand.Bytes(len(blk.EvidenceHash))
		}, true},
		{"Incorrect block protocol version", func(blk *Block) {
			blk.Version.Block = 1
		}, true},
		{"Missing LastCommit", func(blk *Block) {
			blk.LastCommit = nil
		}, true},
		{"Invalid LastCommit", func(blk *Block) {
			blk.LastCommit = NewCommit(-1, 0, *voteSet.maj23, nil, nil)
		}, true},
		{"Invalid Evidence", func(blk *Block) {
			emptyEv := &DuplicateVoteEvidence{}
			blk.Evidence = []Evidence{emptyEv}
		}, true},
	}

	for i, tc := range testCases {
		tcRun := tc
		j := i
		t.Run(tcRun.testName, func(t *testing.T) {
			block := MakeBlock(h, txs, commit, evList)
			block.ProposerProTxHash = valSet.Proposer().ProTxHash
			tcRun.malleateBlock(block)
			err = block.ValidateBasic()
			assert.Equal(t, tcRun.expErr, err != nil, "#%d: %v", j, err)
		})
	}
}

func TestBlockHash(t *testing.T) {
	assert.Nil(t, (*Block)(nil).Hash())
	assert.Nil(t, MakeBlock(int64(3), []Tx{Tx("Hello World")}, nil, nil).Hash())
}

func TestBlockMakePartSet(t *testing.T) {
	bps, err := (*Block)(nil).MakePartSet(2)
	assert.Error(t, err)
	assert.Nil(t, bps)

	partSet, err := MakeBlock(int64(3), []Tx{Tx("Hello World")}, nil, nil).MakePartSet(1024)
	require.NoError(t, err)

	assert.NotNil(t, partSet)
	assert.EqualValues(t, 1, partSet.Total())
}

func TestBlockMakePartSetWithEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bps, err := (*Block)(nil).MakePartSet(2)
	assert.Error(t, err)
	assert.Nil(t, bps)

	lastID := makeBlockIDRandom()
	h := int64(3)

	voteSet, valSet, vals := randVoteSet(ctx, t, h-1, 1, tmproto.PrecommitType, 10)
	commit, err := makeCommit(ctx, lastID, h-1, 1, voteSet, vals)
	require.NoError(t, err)

	ev, err := NewMockDuplicateVoteEvidenceWithValidator(ctx, h, time.Now(), vals[0], "block-test-chain", valSet.QuorumType,
		valSet.QuorumHash)
	require.NoError(t, err)
	evList := []Evidence{ev}

	partSet, err := MakeBlock(h, []Tx{Tx("Hello World :):)")}, commit, evList).MakePartSet(512)
	require.NoError(t, err)

	assert.EqualValues(t, 3, partSet.Total())
}

func TestBlockHashesTo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	assert.False(t, (*Block)(nil).HashesTo(nil))

	lastID := makeBlockIDRandom()
	h := int64(3)

	voteSet, valSet, vals := randVoteSet(ctx, t, h-1, 1, tmproto.PrecommitType, 10)
	commit, err := makeCommit(ctx, lastID, h-1, 1, voteSet, vals)
	require.NoError(t, err)

	ev, err := NewMockDuplicateVoteEvidenceWithValidator(ctx, h, time.Now(), vals[0], "block-test-chain", valSet.QuorumType,
		valSet.QuorumHash)
	require.NoError(t, err)
	evList := []Evidence{ev}

	block := MakeBlock(h, []Tx{Tx("Hello World")}, commit, evList)
	block.ValidatorsHash = valSet.Hash()
	assert.False(t, block.HashesTo([]byte{}))
	assert.False(t, block.HashesTo([]byte("something else")))
	assert.True(t, block.HashesTo(block.Hash()))
}

func TestBlockSize(t *testing.T) {
	size := MakeBlock(int64(3), []Tx{Tx("Hello World")}, nil, nil).Size()
	if size <= 0 {
		t.Fatal("Size of the block is zero or negative")
	}
}

// Given a block with more than `maxLoggedTxs` transactions,
// when we marshal it for logging,
// then we should see short hashes of the first `maxLoggedTxs` transactions in the log message, ending with "..."
func TestBlockMarshalZerolog(t *testing.T) {
	ctx := context.Background()
	logger := log.NewTestingLogger(t)

	txs := make(Txs, 0, 2*maxLoggedTxs)
	expectTxs := make(Txs, 0, maxLoggedTxs)
	for i := 0; i < 2*maxLoggedTxs; i++ {
		txs = append(txs, Tx(fmt.Sprintf("tx%d", i)))
		if i < maxLoggedTxs {
			expectTxs = append(expectTxs, txs[i])
		}
	}

	block := MakeBlock(1, txs, randCommit(ctx, t, 1, RandStateID()), nil)

	// define assertions
	expected := fmt.Sprintf(",\"txs\":{\"num_txs\":%d,\"hashes\":[", 2*maxLoggedTxs)
	for i := 0; i < maxLoggedTxs; i++ {
		expected += "\"" + expectTxs[i].Hash().ShortString() + "\","
	}
	expected += "\"...\"]}"
	logger.AssertContains(expected)

	// execute test
	logger.Info("test block", "block", block)
}

func TestBlockString(t *testing.T) {
	assert.Equal(t, "nil-Block", (*Block)(nil).String())
	assert.Equal(t, "nil-Block", (*Block)(nil).StringIndented(""))
	assert.Equal(t, "nil-Block", (*Block)(nil).StringShort())

	block := MakeBlock(int64(3), []Tx{Tx("Hello World")}, nil, nil)
	assert.NotEqual(t, "nil-Block", block.String())
	assert.NotEqual(t, "nil-Block", block.StringIndented(""))
	assert.NotEqual(t, "nil-Block", block.StringShort())
}

func makeBlockIDRandom() BlockID {
	return BlockID{
		Hash:          tmrand.Bytes(crypto.HashSize),
		PartSetHeader: PartSetHeader{123, tmrand.Bytes(crypto.HashSize)},
		StateID:       RandStateID().Hash(),
	}
}

func makeBlockID(hash tmbytes.HexBytes, partSetSize uint32, partSetHash tmbytes.HexBytes, stateID tmbytes.HexBytes) BlockID {
	if stateID == nil {
		stateID = RandStateID().Hash()
	}
	return BlockID{
		Hash: hash.Copy(),
		PartSetHeader: PartSetHeader{
			Total: partSetSize,
			Hash:  partSetHash.Copy(),
		},
		StateID: stateID.Copy(),
	}
}

var nilBytes []byte

// This follows RFC-6962, i.e. `echo -n "" | sha256sum`
var emptyBytes = []byte{0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, 0x9a, 0xfb, 0xf4, 0xc8,
	0x99, 0x6f, 0xb9, 0x24, 0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c, 0xa4, 0x95, 0x99, 0x1b,
	0x78, 0x52, 0xb8, 0x55}

func TestNilHeaderHashDoesntCrash(t *testing.T) {
	assert.Equal(t, nilBytes, []byte((*Header)(nil).Hash()))
	assert.Equal(t, nilBytes, []byte((new(Header)).Hash()))
}

func TestNilDataHashDoesntCrash(t *testing.T) {
	assert.Equal(t, emptyBytes, []byte((*Data)(nil).Hash()))
	assert.Equal(t, emptyBytes, []byte(new(Data).Hash()))
}

func TestCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lastID := makeBlockIDRandom()
	h := int64(3)

	voteSet, _, vals := randVoteSet(ctx, t, h-1, 1, tmproto.PrecommitType, 10)
	commit, err := makeCommit(ctx, lastID, h-1, 1, voteSet, vals)
	require.NoError(t, err)

	assert.Equal(t, h-1, commit.Height)
	assert.EqualValues(t, 1, commit.Round)
	assert.Equal(t, tmproto.PrecommitType, tmproto.SignedMsgType(commit.Type()))

	require.NotNil(t, commit.ThresholdBlockSignature)
	// TODO replace an assertion with a correct one
	//assert.Equal(t, voteWithoutExtension(voteSet.GetByIndex(0)), commit.GetByIndex(0))
	assert.True(t, commit.IsCommit())
}

func TestCommitValidateBasic(t *testing.T) {
	const height int64 = 5
	testCases := []struct {
		testName       string
		malleateCommit func(*Commit)
		expectErr      bool
	}{
		{"Random Commit", func(com *Commit) {}, false},
		{"Incorrect block signature", func(com *Commit) { com.ThresholdBlockSignature = []byte{0} }, true},
		{"Incorrect height", func(com *Commit) { com.Height = int64(-100) }, true},
		{"Incorrect round", func(com *Commit) { com.Round = -100 }, true},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			stateID := RandStateID()
			stateID.Height = uint64(height - 1)
			com := randCommit(ctx, t, height-1, stateID)

			tc.malleateCommit(com)
			assert.Equal(t, tc.expectErr, com.ValidateBasic() != nil, "Validate Basic had an unexpected result")
		})
	}
}

func TestMaxCommitBytes(t *testing.T) {
	// check size with a single commit
	commit := &Commit{
		Height: math.MaxInt64,
		Round:  math.MaxInt32,
		BlockID: BlockID{
			Hash: crypto.Checksum([]byte("blockID_hash")),
			PartSetHeader: PartSetHeader{
				Total: math.MaxInt32,
				Hash:  crypto.Checksum([]byte("blockID_part_set_header_hash")),
			},
			StateID: RandStateID().Hash(),
		},
		QuorumHash:              crypto.Checksum([]byte("QuorumHash")),
		ThresholdBlockSignature: crypto.CRandBytes(SignatureSize),
	}

	bIDProto := commit.BlockID.ToProto()
	assert.Equal(t, 110, bIDProto.Size(), "blockID size")

	pb := commit.ToProto()
	pbSize := int64(pb.Size())
	assert.EqualValues(t, MaxCommitOverheadBytes, pbSize)
}

func TestHeaderHash(t *testing.T) {
	ts := uint64(time.Date(2022, 3, 4, 5, 6, 7, 8, time.UTC).UnixMilli())

	testCases := []struct {
		desc       string
		header     *Header
		expectHash tmbytes.HexBytes
	}{
		{
			desc: "Generates expected hash", header: &Header{
				Version:               version.Consensus{Block: 1, App: 2},
				ChainID:               "chainId",
				Height:                3,
				CoreChainLockedHeight: 1,
				Time:                  time.Date(2019, 10, 13, 16, 14, 44, 0, time.UTC),
				LastBlockID: makeBlockID(
					make([]byte, crypto.HashSize),
					6, make([]byte, crypto.HashSize),
					tmproto.StateID{
						AppVersion:            StateIDVersion,
						Height:                3,
						AppHash:               crypto.Checksum([]byte("app_hash")),
						CoreChainLockedHeight: 1,
						Time:                  ts,
					}.Hash(),
				),
				LastCommitHash:     crypto.Checksum([]byte("last_commit_hash")),
				DataHash:           crypto.Checksum([]byte("data_hash")),
				ValidatorsHash:     crypto.Checksum([]byte("validators_hash")),
				NextValidatorsHash: crypto.Checksum([]byte("next_validators_hash")),
				ConsensusHash:      crypto.Checksum([]byte("consensus_hash")),
				NextConsensusHash:  crypto.Checksum([]byte("next_consensus_hash")),
				AppHash:            crypto.Checksum([]byte("app_hash")),
				ResultsHash:        crypto.Checksum([]byte("last_results_hash")),
				EvidenceHash:       crypto.Checksum([]byte("evidence_hash")),
				ProposerProTxHash:  crypto.ProTxHashFromSeedBytes([]byte("proposer_pro_tx_hash")),
				ProposedAppVersion: 1,
			},
			expectHash: hexBytesFromString(t, "FF24DDAB9E1550BEB40AB7AD432A4D577560E9B87A80C7BB86E75263974B87E0"),
		},
		{
			"nil header yields nil",
			nil,
			nil,
		},
		{
			"nil ValidatorsHash yields nil",
			&Header{
				Version:               version.Consensus{Block: 1, App: 2},
				ChainID:               "chainId",
				Height:                3,
				CoreChainLockedHeight: 1,
				Time:                  time.Date(2019, 10, 13, 16, 14, 44, 0, time.UTC),
				LastBlockID:           makeBlockID(make([]byte, crypto.HashSize), 6, make([]byte, crypto.HashSize), RandStateID().Hash()),
				LastCommitHash:        crypto.Checksum([]byte("last_commit_hash")),
				DataHash:              crypto.Checksum([]byte("data_hash")),
				ValidatorsHash:        nil,
				NextValidatorsHash:    crypto.Checksum([]byte("next_validators_hash")),
				ConsensusHash:         crypto.Checksum([]byte("consensus_hash")),
				AppHash:               crypto.Checksum([]byte("app_hash")),
				ResultsHash:           crypto.Checksum([]byte("results_hash")),
				EvidenceHash:          crypto.Checksum([]byte("evidence_hash")),
				ProposerProTxHash:     crypto.ProTxHashFromSeedBytes([]byte("proposer_pro_tx_hash")),
				ProposedAppVersion:    1,
			},
			nil,
		},
	}
	for _, tc := range testCases {
		tcRun := tc
		t.Run(tcRun.desc, func(t *testing.T) {
			assert.Equal(t, tcRun.expectHash, tcRun.header.Hash())

			// We also make sure that all fields are hashed in struct order, and that all
			// fields in the test struct are non-zero.
			if tcRun.header != nil && tcRun.expectHash != nil {
				byteSlices := [][]byte{}

				s := reflect.ValueOf(*tcRun.header)
				for i := 0; i < s.NumField(); i++ {
					f := s.Field(i)

					assert.False(t, f.IsZero(), "Found zero-valued field %v",
						s.Type().Field(i).Name)

					switch f := f.Interface().(type) {
					case int64, uint32, uint64, tmbytes.HexBytes, string:
						byteSlices = append(byteSlices, cdcEncode(f))
					case time.Time:
						bz, err := gogotypes.StdTimeMarshal(f)
						require.NoError(t, err)
						byteSlices = append(byteSlices, bz)
					case version.Consensus:
						pbc := tmversion.Consensus{
							Block: f.Block,
							App:   f.App,
						}
						bz, err := pbc.Marshal()
						require.NoError(t, err)
						byteSlices = append(byteSlices, bz)
					case BlockID:
						pbbi := f.ToProto()
						bz, err := pbbi.Marshal()
						require.NoError(t, err)
						byteSlices = append(byteSlices, bz)
					default:
						t.Errorf("unknown type %T", f)
					}
				}
				assert.Equal(t,
					tmbytes.HexBytes(merkle.HashFromByteSlices(byteSlices)), tcRun.header.Hash())
			}
		})
	}
}

func TestMaxHeaderBytes(t *testing.T) {
	// Construct a UTF-8 string of MaxChainIDLen length using the supplementary
	// characters.
	// Each supplementary character takes 4 bytes.
	// http://www.i18nguy.com/unicode/supplementary-test.html
	maxChainID := ""
	for i := 0; i < MaxChainIDLen; i++ {
		maxChainID += "𠜎"
	}

	// time is varint encoded so need to pick the max.
	// year int, month Month, day, hour, min, sec, nsec int, loc *Location
	timestamp := time.Date(math.MaxInt64, 0, 0, 0, 0, 0, math.MaxInt64, time.UTC)

	h := Header{
		Version:               version.Consensus{Block: math.MaxInt64, App: math.MaxUint64},
		ChainID:               maxChainID,
		Height:                math.MaxInt64,
		Time:                  timestamp,
		LastBlockID:           makeBlockID(make([]byte, crypto.HashSize), math.MaxInt32, make([]byte, crypto.HashSize), RandStateID().Hash()),
		LastCommitHash:        crypto.Checksum([]byte("last_commit_hash")),
		DataHash:              crypto.Checksum([]byte("data_hash")),
		ValidatorsHash:        crypto.Checksum([]byte("validators_hash")),
		NextValidatorsHash:    crypto.Checksum([]byte("next_validators_hash")),
		ConsensusHash:         crypto.Checksum([]byte("consensus_hash")),
		NextConsensusHash:     crypto.Checksum([]byte("next_consensus_hash")),
		AppHash:               crypto.Checksum([]byte("app_hash")),
		ResultsHash:           crypto.Checksum([]byte("results_hash")),
		EvidenceHash:          crypto.Checksum([]byte("evidence_hash")),
		CoreChainLockedHeight: math.MaxUint32,
		ProposerProTxHash:     crypto.ProTxHashFromSeedBytes([]byte("proposer_pro_tx_hash")),
		ProposedAppVersion:    math.MaxUint64,
	}

	bz, err := h.ToProto().Marshal()
	require.NoError(t, err)

	assert.EqualValues(t, MaxHeaderBytes, int64(len(bz)))
}

func randCommit(ctx context.Context, t *testing.T, height int64, stateID tmproto.StateID) *Commit {
	t.Helper()

	blockID := makeBlockID(
		tmrand.Bytes(crypto.HashSize),
		123,
		tmrand.Bytes(crypto.HashSize),
		stateID.Hash(),
	)
	voteSet, _, vals := randVoteSet(ctx, t, height, 1, tmproto.PrecommitType, 10)
	commit, err := makeCommit(ctx, blockID, height, 1, voteSet, vals)

	require.NoError(t, err)

	return commit
}

func hexBytesFromString(t *testing.T, s string) tmbytes.HexBytes {
	t.Helper()

	b, err := hex.DecodeString(s)
	require.NoError(t, err)

	return b
}

func TestBlockMaxDataBytes(t *testing.T) {
	ctx := context.Background()
	height := int64(math.MaxInt64)
	stateID := RandStateID()
	stateID.Height = uint64(height)
	commit := randCommit(ctx, t, height, stateID)
	require.NotNil(t, commit)

	// minBlockSize is minimum correct size of a block
	const minBlockSize = 1371

	testCases := []struct {
		maxBytes      int64
		lastCommit    *Commit
		evidenceBytes int64
		expectError   bool
		result        int64
	}{
		0: {-10, commit, 1, true, 0},
		1: {10, commit, 1, true, 0},
		2: {minBlockSize - 1, commit, 0, true, 0},
		3: {minBlockSize, commit, 1, true, 0},
		4: {minBlockSize, commit, 0, false, 0},
		5: {minBlockSize + 1, commit, 0, false, 1},
		6: {minBlockSize + 1, commit, 1, false, 0},
		7: {minBlockSize + 1, commit, 2, true, 0},
		8: {minBlockSize + 2, commit, 2, false, 0},
		9: {minBlockSize + 29, commit, 2, false, 27},
	}
	// An extra 33 bytes (32 for sig, 1 for proto encoding are needed for BLS compared to edwards per validator

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d:%d_%d", i, tc.maxBytes, tc.evidenceBytes), func(t *testing.T) {
			maxDataBytes, err := MaxDataBytes(tc.maxBytes, tc.lastCommit, tc.evidenceBytes)
			if tc.expectError {
				assert.Error(t, err, "#%+v, %d", tc, maxDataBytes)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.result, maxDataBytes, "#%+v", tc)
			}
		})
	}
}

func TestBlockMaxDataBytesNoEvidence(t *testing.T) {
	// minBlockSize is minimum correct size of a block
	const minBlockSize = 1129

	testCases := []struct {
		maxBytes int64
		errs     bool
		result   int64
	}{
		0: {-10, true, 0},
		1: {10, true, 0},
		2: {minBlockSize - 1, true, 0},
		3: {minBlockSize, false, 0},
		4: {minBlockSize + 1, false, 1},
		5: {minBlockSize + 34, false, 34},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d:%d", i, tc.maxBytes), func(t *testing.T) {
			maxDataBytes, err := MaxDataBytesNoEvidence(tc.maxBytes)
			if tc.errs {
				assert.Error(t, err, "%+v (%d)", tc, maxDataBytes)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.result, maxDataBytes, "%+v", tc)
			}
		})
	}
}

func TestCommitToVoteSetWithVotesForNilBlock(t *testing.T) {
	blockID := makeBlockID(
		[]byte("blockhash"),
		1000, []byte("partshash"),
		RandStateID().Hash(),
	)

	const (
		height = int64(3)
		round  = 0
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// all votes below use height - 1
	type commitVoteTest struct {
		blockIDs      []BlockID
		numVotes      []int // must sum to numValidators
		numValidators int
		valid         bool
	}

	testCases := []commitVoteTest{
		{[]BlockID{blockID, {}}, []int{67, 33}, 100, true},
	}

	for _, tc := range testCases {
		voteSet, valSet, vals := randVoteSet(ctx, t, height-1, round, tmproto.PrecommitType, tc.numValidators)

		vi := int32(0)
		for n := range tc.blockIDs {
			for i := 0; i < tc.numVotes[n]; i++ {
				proTxHash, err := vals[vi].GetProTxHash(ctx)
				require.NoError(t, err)
				vote := &Vote{
					ValidatorProTxHash: proTxHash,
					ValidatorIndex:     vi,
					Height:             height - 1,
					Round:              round,
					Type:               tmproto.PrecommitType,
					BlockID:            tc.blockIDs[n],
				}

				added, err := signAddVote(ctx, vals[vi], vote, voteSet)
				assert.NoError(t, err)
				assert.True(t, added)

				vi++
			}
		}

		if tc.valid {
			commit := voteSet.MakeCommit() // panics without > 2/3 valid votes
			assert.NotNil(t, commit)
			err := valSet.VerifyCommit(voteSet.ChainID(), blockID, height-1, commit)
			assert.NoError(t, err)
		} else {
			assert.Panics(t, func() { voteSet.MakeCommit() })
		}
	}
}

func TestBlockIDValidateBasic(t *testing.T) {
	validBlockID := BlockID{
		Hash: tmrand.Bytes(crypto.HashSize),
		PartSetHeader: PartSetHeader{
			Total: 1,
			Hash:  tmrand.Bytes(crypto.HashSize),
		},
		StateID: tmproto.StateID{}.Hash(),
	}

	invalidBlockID := BlockID{
		Hash: []byte{0},
		PartSetHeader: PartSetHeader{
			Total: 1,
			Hash:  []byte{0},
		},
		StateID: []byte("too short"),
	}

	testCases := []struct {
		testName             string
		blockIDHash          tmbytes.HexBytes
		blockIDPartSetHeader PartSetHeader
		blockIDStateID       tmbytes.HexBytes
		expectErr            bool
	}{
		{
			testName:    "Valid NIL BlockID",
			blockIDHash: []byte{},
			blockIDPartSetHeader: PartSetHeader{
				Total: 0,
				Hash:  []byte{},
			},
			blockIDStateID: tmproto.StateID{}.Hash(),
			expectErr:      false,
		},
		{
			testName:             "Valid BlockID",
			blockIDHash:          validBlockID.Hash,
			blockIDPartSetHeader: validBlockID.PartSetHeader,
			blockIDStateID:       validBlockID.StateID,
			expectErr:            false,
		},
		{
			testName:             "Invalid Hash",
			blockIDHash:          invalidBlockID.Hash,
			blockIDPartSetHeader: validBlockID.PartSetHeader,
			blockIDStateID:       validBlockID.StateID,
			expectErr:            true,
		},
		{
			testName:             "Invalid PartSetHeader",
			blockIDHash:          validBlockID.Hash,
			blockIDPartSetHeader: invalidBlockID.PartSetHeader,
			blockIDStateID:       validBlockID.StateID,
			expectErr:            true,
		},
		{
			testName:             "Invalid StateID",
			blockIDHash:          validBlockID.Hash,
			blockIDPartSetHeader: validBlockID.PartSetHeader,
			blockIDStateID:       invalidBlockID.StateID,
			expectErr:            true,
		},
	}

	for _, tcRun := range testCases {
		t.Run(tcRun.testName, func(t *testing.T) {
			blockID := BlockID{
				Hash:          tcRun.blockIDHash,
				PartSetHeader: tcRun.blockIDPartSetHeader,
				StateID:       tcRun.blockIDStateID,
			}
			err := blockID.ValidateBasic()
			if tcRun.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBlockProtoBuf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := mrand.Int63()
	stateID := RandStateID()
	stateID.Height = uint64(h - 1)
	c1 := randCommit(ctx, t, h-1, stateID)
	b1 := MakeBlock(h, []Tx{Tx([]byte{1})}, &Commit{}, []Evidence{})
	b1.ProposerProTxHash = tmrand.Bytes(crypto.DefaultHashSize)

	b2 := MakeBlock(h, []Tx{Tx([]byte{1})}, c1, []Evidence{})
	b2.ProposerProTxHash = tmrand.Bytes(crypto.DefaultHashSize)
	evidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	evi, err := NewMockDuplicateVoteEvidence(
		ctx,
		h,
		evidenceTime,
		"block-test-chain",
		btcjson.LLMQType_5_60,
		crypto.RandQuorumHash(),
	)
	require.NoError(t, err)
	b2.Evidence = EvidenceList{evi}
	b2.EvidenceHash = b2.Evidence.Hash()

	b3 := MakeBlock(h, []Tx{}, c1, []Evidence{})
	b3.ProposerProTxHash = tmrand.Bytes(crypto.DefaultHashSize)
	testCases := []struct {
		msg      string
		b1       *Block
		expPass  bool
		expPass2 bool
	}{
		{"nil block", nil, false, false},
		{"b1", b1, true, true},
		{"b2", b2, true, true},
		{"b3", b3, true, true},
	}
	for _, tc := range testCases {
		pb, err := tc.b1.ToProto()
		if tc.expPass {
			require.NoError(t, err, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}

		block, err := BlockFromProto(pb)
		if tc.expPass2 {
			require.NoError(t, err, tc.msg)
			require.EqualValues(t, tc.b1.Header, block.Header, tc.msg)
			require.EqualValues(t, tc.b1.Data, block.Data, tc.msg)
			require.EqualValues(t, tc.b1.Evidence, block.Evidence, tc.msg)
			require.EqualValues(t, *tc.b1.LastCommit, *block.LastCommit, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

func TestDataProtoBuf(t *testing.T) {
	data := &Data{Txs: Txs{Tx([]byte{1}), Tx([]byte{2}), Tx([]byte{3})}}
	data2 := &Data{Txs: Txs{}}
	testCases := []struct {
		msg     string
		data1   *Data
		expPass bool
	}{
		{"success", data, true},
		{"success data2", data2, true},
	}
	for _, tc := range testCases {
		protoData := tc.data1.ToProto()
		d, err := DataFromProto(&protoData)
		if tc.expPass {
			require.NoError(t, err, tc.msg)
			require.EqualValues(t, tc.data1, &d, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

// exposed for testing
func MakeRandHeader() Header {
	chainID := "test"
	t := time.Now()
	height := mrand.Int63()
	randBytes := tmrand.Bytes(crypto.HashSize)
	randProTxHash := tmrand.Bytes(crypto.DefaultHashSize)
	h := Header{
		Version:            version.Consensus{Block: version.BlockProtocol, App: 1},
		ChainID:            chainID,
		Height:             height,
		Time:               t,
		LastBlockID:        BlockID{},
		LastCommitHash:     randBytes,
		DataHash:           randBytes,
		ValidatorsHash:     randBytes,
		NextValidatorsHash: randBytes,
		ConsensusHash:      randBytes,
		AppHash:            randBytes,

		ResultsHash: randBytes,

		EvidenceHash:      randBytes,
		ProposerProTxHash: randProTxHash,
	}

	return h
}

func TestHeaderProto(t *testing.T) {
	h1 := MakeRandHeader()
	tc := []struct {
		msg     string
		h1      *Header
		expPass bool
	}{
		{"success", &h1, true},
		{"failure empty Header", &Header{}, false},
	}

	for _, tt := range tc {
		tt := tt
		t.Run(tt.msg, func(t *testing.T) {
			pb := tt.h1.ToProto()
			h, err := HeaderFromProto(pb)
			if tt.expPass {
				require.NoError(t, err, tt.msg)
				require.Equal(t, tt.h1, &h, tt.msg)
			} else {
				require.Error(t, err, tt.msg)
			}

		})
	}
}

func TestBlockIDProtoBuf(t *testing.T) {
	blockID := makeBlockID(
		crypto.Checksum([]byte("hash")),
		2,
		crypto.Checksum([]byte("part_set_hash")),
		RandStateID().Hash(),
	)
	testCases := []struct {
		msg     string
		bid1    *BlockID
		expPass bool
	}{
		{"success", &blockID, true},
		{"success empty", &BlockID{}, true},
		{"failure BlockID nil", nil, false},
	}
	for _, tc := range testCases {
		protoBlockID := tc.bid1.ToProto()

		bi, err := BlockIDFromProto(&protoBlockID)
		if tc.expPass {
			require.NoError(t, err)
			require.Equal(t, tc.bid1, bi, tc.msg)
		} else {
			require.NotEqual(t, tc.bid1, bi, tc.msg)
		}
	}
}

func TestSignedHeaderProtoBuf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := MakeRandHeader()
	stateID := RandStateID()
	stateID.Height = uint64(h.Height)
	commit := randCommit(ctx, t, h.Height, stateID)

	sh := SignedHeader{Header: &h, Commit: commit}

	testCases := []struct {
		msg     string
		sh1     *SignedHeader
		expPass bool
	}{
		{"empty SignedHeader 2", &SignedHeader{}, true},
		{"success", &sh, true},
		{"failure nil", nil, false},
	}
	for _, tc := range testCases {
		protoSignedHeader := tc.sh1.ToProto()

		sh, err := SignedHeaderFromProto(protoSignedHeader)

		if tc.expPass {
			require.NoError(t, err, tc.msg)
			require.Equal(t, tc.sh1, sh, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

func TestBlockIDEquals(t *testing.T) {
	var (
		stateID1                = RandStateID().Hash()
		stateID2                = RandStateID().Hash()
		blockID                 = makeBlockID([]byte("hash"), 2, []byte("part_set_hash"), stateID1)
		blockIDDuplicate        = makeBlockID([]byte("hash"), 2, []byte("part_set_hash"), stateID1)
		blockIDDifferentHash    = makeBlockID([]byte("different_hash"), 2, []byte("part_set_hash"), stateID1)
		blockIDDifferentStateID = makeBlockID([]byte("hash"), 2, []byte("part_set_hash"), stateID2)

		blockIDEmpty = BlockID{}
	)

	assert.True(t, blockID.Equals(blockIDDuplicate))
	assert.False(t, blockID.Equals(blockIDDifferentHash))
	assert.False(t, blockID.Equals(blockIDEmpty))
	assert.True(t, blockIDEmpty.Equals(blockIDEmpty)) //nolint: gocritic
	assert.False(t, blockIDEmpty.Equals(blockIDDifferentHash))
	assert.False(t, blockIDEmpty.Equals(blockIDDifferentStateID))
}

// StateID tests

// TODO: Move to separate file

func TestStateID_Copy(t *testing.T) {
	state1 := RandStateID()
	state2 := state1.Copy()
	assert.Equal(t, state1, state2)

	state2.AppHash[5] = 0x12
	assert.NotEqual(t, state1, state2)
}

func TestHeader_ValidateBasic(t *testing.T) {
	testCases := []struct {
		name      string
		header    Header
		expectErr bool
		errString string
	}{
		{
			"invalid version block",
			Header{Version: version.Consensus{Block: version.BlockProtocol + 1}},
			true, "block protocol is incorrect",
		},
		{
			"invalid chain ID length",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen+1)),
			},
			true, "chainID is too long",
		},
		{
			"invalid height (negative)",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  -1,
			},
			true, "negative Height",
		},
		{
			"invalid height (zero)",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  0,
			},
			true, "zero Height",
		},
		{
			"invalid block ID hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize+1),
				},
			},
			true, "wrong Hash",
		},
		{
			"invalid block ID parts header hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize+1),
					},
				},
			},
			true, "wrong PartSetHeader",
		},
		{
			"invalid last commit hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash: make([]byte, crypto.HashSize+1),
			},
			true, "wrong LastCommitHash",
		},
		{
			"invalid data hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash: make([]byte, crypto.HashSize),
				DataHash:       make([]byte, crypto.HashSize+1),
			},
			true, "wrong DataHash",
		},
		{
			"invalid evidence hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash: make([]byte, crypto.HashSize),
				DataHash:       make([]byte, crypto.HashSize),
				EvidenceHash:   make([]byte, crypto.HashSize+1),
			},
			true, "wrong EvidenceHash",
		},
		{
			"invalid proposer protxhash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash: make([]byte, crypto.HashSize),
				DataHash:       make([]byte, crypto.HashSize),
				EvidenceHash:   make([]byte, crypto.HashSize),

				ProposerProTxHash: make([]byte, crypto.ProTxHashSize+1),
			},
			true, "invalid ProposerProTxHash length; got: 33, expected: 32",
		},
		{
			"invalid validator hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash:    make([]byte, crypto.HashSize),
				DataHash:          make([]byte, crypto.HashSize),
				EvidenceHash:      make([]byte, crypto.HashSize),
				ProposerProTxHash: make([]byte, crypto.ProTxHashSize),
				ValidatorsHash:    make([]byte, crypto.HashSize+1),
			},
			true, "wrong ValidatorsHash",
		},
		{
			"invalid next validator hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash:     make([]byte, crypto.HashSize),
				DataHash:           make([]byte, crypto.HashSize),
				EvidenceHash:       make([]byte, crypto.HashSize),
				ProposerProTxHash:  make([]byte, crypto.ProTxHashSize),
				ValidatorsHash:     make([]byte, crypto.HashSize),
				NextValidatorsHash: make([]byte, crypto.HashSize+1),
			},
			true, "wrong NextValidatorsHash",
		},
		{
			"invalid consensus hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash:     make([]byte, crypto.HashSize),
				DataHash:           make([]byte, crypto.HashSize),
				EvidenceHash:       make([]byte, crypto.HashSize),
				ProposerProTxHash:  make([]byte, crypto.ProTxHashSize),
				ValidatorsHash:     make([]byte, crypto.HashSize),
				NextValidatorsHash: make([]byte, crypto.HashSize),
				ConsensusHash:      make([]byte, crypto.HashSize+1),
			},
			true, "wrong ConsensusHash",
		},
		{
			"invalid last results hash",
			Header{
				Version: version.Consensus{Block: version.BlockProtocol},
				ChainID: string(make([]byte, MaxChainIDLen)),
				Height:  1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash:     make([]byte, crypto.HashSize),
				DataHash:           make([]byte, crypto.HashSize),
				EvidenceHash:       make([]byte, crypto.HashSize),
				ProposerProTxHash:  make([]byte, crypto.ProTxHashSize),
				ValidatorsHash:     make([]byte, crypto.HashSize),
				NextValidatorsHash: make([]byte, crypto.HashSize),
				ConsensusHash:      make([]byte, crypto.HashSize),
				ResultsHash:        make([]byte, crypto.HashSize+1),
			},
			true, "wrong LastResultsHash",
		},
		{
			"valid header",
			Header{
				Version:               version.Consensus{Block: version.BlockProtocol},
				ChainID:               string(make([]byte, MaxChainIDLen)),
				Height:                1,
				CoreChainLockedHeight: 1,
				LastBlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
					StateID: RandStateID().Hash(),
				},
				LastCommitHash:     make([]byte, crypto.HashSize),
				DataHash:           make([]byte, crypto.HashSize),
				EvidenceHash:       make([]byte, crypto.HashSize),
				ProposerProTxHash:  make([]byte, crypto.ProTxHashSize),
				ValidatorsHash:     make([]byte, crypto.HashSize),
				NextValidatorsHash: make([]byte, crypto.HashSize),
				ConsensusHash:      make([]byte, crypto.HashSize),
				ResultsHash:        make([]byte, crypto.HashSize),
			},
			false, "",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			err := tc.header.ValidateBasic()
			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errString)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestStateID_ValidateBasic(t *testing.T) {

	tests := []struct {
		name    string
		stateID tmproto.StateID
		wantErr string
	}{
		{
			name: "zero height - allowed for genesis block",
			stateID: tmproto.StateID{
				AppVersion: StateIDVersion,
				Height:     0,
				AppHash:    tmrand.Bytes(crypto.DefaultAppHashSize),
				Time:       uint64(tmtime.Now().UnixMilli()),
			},
			wantErr: "",
		},
		{
			name: "apphash default",
			stateID: tmproto.StateID{
				AppVersion: StateIDVersion,
				Height:     12,
				AppHash:    tmrand.Bytes(crypto.DefaultAppHashSize),
				Time:       uint64(tmtime.Now().UnixMilli()),
			},
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateID := tc.stateID
			err := stateID.ValidateBasic()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func TestCommit_ValidateBasic(t *testing.T) {
	testCases := []struct {
		name      string
		commit    *Commit
		expectErr bool
		errString string
	}{
		{
			"invalid height",
			&Commit{Height: -1},
			true, "negative Height",
		},
		{
			"invalid round",
			&Commit{Height: 1, Round: -1},
			true, "negative Round",
		},
		{
			"invalid block ID",
			&Commit{
				Height:  1,
				Round:   1,
				BlockID: BlockID{},
			},
			true, "commit cannot be for nil block",
		},
		{
			"invalid block signature",
			&Commit{
				Height: 1,
				Round:  1,
				BlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
				},
				ThresholdBlockSignature: make([]byte, bls12381.SignatureSize+1),
			},
			true, "block threshold signature is wrong size",
		},
		{
			"valid commit",
			&Commit{
				Height: 1,
				Round:  1,
				BlockID: BlockID{
					Hash: make([]byte, crypto.HashSize),
					PartSetHeader: PartSetHeader{
						Hash: make([]byte, crypto.HashSize),
					},
				},
				ThresholdBlockSignature: make([]byte, bls12381.SignatureSize),
			},
			false, "",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			err := tc.commit.ValidateBasic()
			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errString)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHeaderHashVector(t *testing.T) {
	chainID := "test"
	h := Header{
		Version:            version.Consensus{Block: 1, App: 1},
		ChainID:            chainID,
		Height:             50,
		Time:               time.Date(math.MaxInt64, 0, 0, 0, 0, 0, math.MaxInt64, time.UTC),
		LastBlockID:        BlockID{},
		LastCommitHash:     []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		DataHash:           []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		ValidatorsHash:     []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		NextValidatorsHash: []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		ConsensusHash:      []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		NextConsensusHash:  []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		AppHash:            []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),

		ResultsHash: []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),

		EvidenceHash:      []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
		ProposerProTxHash: []byte("f2564c78071e26643ae9b3e2a19fa0dc10d4d9e873aa0be808660123f11a1e78"),
	}

	testCases := []struct {
		header   Header
		expBytes string
	}{
		{header: h, expBytes: "1476e858bfe231d7cd767a9693f63fe6a8757e8c07aba432f338a5ecba2b0342"},
	}

	for _, tc := range testCases {
		hash := tc.header.Hash()
		require.Equal(t, tc.expBytes, hex.EncodeToString(hash))
	}
}
