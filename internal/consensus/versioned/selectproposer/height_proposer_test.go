package selectproposer_test

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

//-------------------------------------------------------------------

func TestProposerSelection1(t *testing.T) {
	fooProTxHash := crypto.ProTxHash(crypto.Checksum([]byte("foo")))
	barProTxHash := crypto.ProTxHash(crypto.Checksum([]byte("bar")))
	bazProTxHash := crypto.ProTxHash(crypto.Checksum([]byte("baz")))
	vset := types.NewValidatorSet([]*types.Validator{
		types.NewTestValidatorGeneratedFromProTxHash(fooProTxHash),
		types.NewTestValidatorGeneratedFromProTxHash(barProTxHash),
		types.NewTestValidatorGeneratedFromProTxHash(bazProTxHash),
	}, bls12381.GenPrivKey().PubKey(), btcjson.LLMQType_5_60, crypto.RandQuorumHash(), true, nil)
	var proposers []string

	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, vset, 0, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	for height := int64(0); height < 99; height++ {
		val := vs.MustGetProposer(height, 0)
		proposers = append(proposers, val.ProTxHash.ShortString())
	}
	expected := `2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B`
	if expected != strings.Join(proposers, " ") {
		t.Errorf("expected sequence of proposers was\n%v\nbut got \n%v", expected, strings.Join(proposers, " "))
	}
}

func TestProposerSelection2(t *testing.T) {
	proTxHashes := make([]crypto.ProTxHash, 3)
	addresses := make([]crypto.Address, 3)
	proTxHashes[0] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	proTxHashes[1] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	proTxHashes[2] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}
	addresses[0] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	addresses[1] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	addresses[2] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	vals, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))
	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, vals.Copy(), 0, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	height := 0

	for ; height < len(proTxHashes)*5; height++ {
		ii := (height) % len(proTxHashes)
		prop := vs.MustGetProposer(int64(height), 0)
		if !bytes.Equal(prop.ProTxHash, vals.Validators[ii].ProTxHash) {
			t.Fatalf("(%d): Expected %X. Got %X", height, vals.Validators[ii].ProTxHash, prop.ProTxHash)
		}
	}

	prop := vs.MustGetProposer(int64(height), 0)
	if !bytes.Equal(prop.ProTxHash, proTxHashes[0]) {
		t.Fatalf("Expected proposer with smallest pro_tx_hash to be first proposer. Got %X", prop.ProTxHash)
	}

	height++
	prop = vs.MustGetProposer(int64(height), 0)
	if !bytes.Equal(prop.ProTxHash, proTxHashes[1]) {
		t.Fatalf("Expected proposer with second smallest pro_tx_hash to be second proposer. Got %X", prop.ProTxHash)
	}
}

func TestProposerSelection3(t *testing.T) {
	const initialHeight = 1

	proTxHashes := make([]crypto.ProTxHash, 4)
	proTxHashes[0] = crypto.Checksum([]byte("avalidator_address12"))
	proTxHashes[1] = crypto.Checksum([]byte("bvalidator_address12"))
	proTxHashes[2] = crypto.Checksum([]byte("cvalidator_address12"))
	proTxHashes[3] = crypto.Checksum([]byte("dvalidator_address12"))

	vset, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))

	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, vset.Copy(), initialHeight, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	// initialize proposers
	proposerOrder := make([]*types.Validator, 4)
	for i := 0; i < 4; i++ {
		proposerOrder[i] = vs.MustGetProposer(int64(i+initialHeight), 0)
	}

	// h for the loop
	// j for the times
	// we should go in order for ever, despite some IncrementProposerPriority with times > 1
	var (
		h int
		j uint32
	)

	vs, err = selectproposer.NewProposerSelector(types.ConsensusParams{}, vset.Copy(), 1, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)
	j = 0
	for h = 1; h <= 10000; h++ {

		got := vs.MustGetProposer(int64(h), 0).ProTxHash
		expected := proposerOrder[j%4].ProTxHash
		if !bytes.Equal(got, expected) {
			t.Fatalf("vset.Proposer (%X) does not match expected proposer (%X) for (%d, %d)", got, expected, h, j)
		}

		round := uint32(rand.Int31n(100))
		require.NoError(t, vs.UpdateHeightRound(int64(h), int32(round)))
		j++ // height proposer strategy only increment by 1 each height, regardless of the rounds
	}
}

func setupTestHeightScore(t *testing.T, genesisHeight int64) ([]crypto.ProTxHash, selectproposer.ProposerSelector) {
	var proTxHashes []crypto.ProTxHash
	for i := byte(1); i <= 5; i++ {
		protx := make([]byte, 32)
		protx[0] = i
		proTxHashes = append(proTxHashes, protx)
	}

	vset, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))

	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, vset.Copy(), genesisHeight, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	return proTxHashes, vs
}

func TestHeightScoreH(t *testing.T) {
	const genesisHeight = 1

	proTxHashes, vs := setupTestHeightScore(t, genesisHeight)

	// test that the proposer changes after the height is updated
	for h := int64(1); h < 100; h++ {
		proposer := vs.MustGetProposer(h, 0)
		pos := (h - genesisHeight) % int64(len(proTxHashes))
		assert.Equal(t, proTxHashes[pos], proposer.ProTxHash, "height %d", h)
		require.NoError(t, vs.UpdateHeightRound(h, 0), "height %d", h)
	}
}

// TestHeightScoreRound tests that round proposers don't affect the height proposers
func TestHeightScoreHR(t *testing.T) {
	const genesisHeight = 1

	proTxHashes, vs := setupTestHeightScore(t, genesisHeight)

	// now test with rounds
	for h := int64(1); h < 10; h++ {
		for r := int32(0); r < 10; r++ {
			proposer := vs.MustGetProposer(h, r)
			pos := (h - genesisHeight + int64(r)) % int64(len(proTxHashes))
			require.Equal(t, proTxHashes[pos], proposer.ProTxHash, "height %d, round %d", h, r)
			require.NoError(t, vs.UpdateHeightRound(h, r), "height %d, round %d", h, r)
		}
	}
}
