package types

import (
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/merkle"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmtime "github.com/dashpay/tenderdash/libs/time"
)

const (
	testPartSize = 65536 // 64KB ...  4096 // 4KB
)

func TestBasicPartSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// Construct random data of size partSize * 100
	nParts := 100
	data := tmrand.Bytes(testPartSize * nParts)
	partSet := NewPartSetFromData(data, testPartSize)

	assert.NotEmpty(t, partSet.Hash())
	assert.EqualValues(t, nParts, partSet.Total())
	assert.Equal(t, nParts, partSet.BitArray().Size())
	assert.True(t, partSet.HashesTo(partSet.Hash()))
	assert.True(t, partSet.IsComplete())
	assert.EqualValues(t, nParts, partSet.Count())
	assert.EqualValues(t, testPartSize*nParts, partSet.ByteSize())

	// Test adding parts to a new partSet.
	partSet2 := NewPartSetFromHeader(partSet.Header())

	assert.True(t, partSet2.HasHeader(partSet.Header()))
	for i := 0; i < int(partSet.Total()); i++ {
		part := partSet.GetPart(i)
		// t.Logf("\n%v", part)
		added, err := partSet2.AddPart(part)
		if !added || err != nil {
			t.Errorf("failed to add part %v, error: %v", i, err)
		}
	}
	// adding part with invalid index
	added, err := partSet2.AddPart(&Part{Index: 10000})
	assert.False(t, added)
	assert.Error(t, err)
	// adding existing part
	added, err = partSet2.AddPart(partSet2.GetPart(0))
	assert.False(t, added)
	assert.NoError(t, err)

	assert.Equal(t, partSet.Hash(), partSet2.Hash())
	assert.EqualValues(t, nParts, partSet2.Total())
	assert.EqualValues(t, nParts*testPartSize, partSet.ByteSize())
	assert.True(t, partSet2.IsComplete())

	// Reconstruct data, assert that they are equal.
	data2Reader := partSet2.GetReader()
	data2, err := io.ReadAll(data2Reader)
	require.NoError(t, err)

	assert.Equal(t, data, data2)
}

func TestWrongProof(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// Construct random data of size partSize * 100
	data := tmrand.Bytes(testPartSize * 100)
	partSet := NewPartSetFromData(data, testPartSize)

	// Test adding a part with wrong data.
	partSet2 := NewPartSetFromHeader(partSet.Header())

	// Test adding a part with wrong trail.
	part := partSet.GetPart(0)
	part.Proof.Aunts[0][0] += byte(0x01)
	added, err := partSet2.AddPart(part)
	if added || err == nil {
		t.Errorf("expected to fail adding a part with bad trail.")
	}

	// Test adding a part with wrong bytes.
	part = partSet.GetPart(1)
	part.Bytes[0] += byte(0x01)
	added, err = partSet2.AddPart(part)
	if added || err == nil {
		t.Errorf("expected to fail adding a part with bad bytes.")
	}
}

func TestPartSetHeaderValidateBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	testCases := []struct {
		testName              string
		malleatePartSetHeader func(*PartSetHeader)
		expectErr             bool
	}{
		{"Good PartSet", func(psHeader *PartSetHeader) {}, false},
		{"Invalid Hash", func(psHeader *PartSetHeader) { psHeader.Hash = make([]byte, 1) }, true},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			data := tmrand.Bytes(testPartSize * 100)
			ps := NewPartSetFromData(data, testPartSize)
			psHeader := ps.Header()
			tc.malleatePartSetHeader(&psHeader)
			assert.Equal(t, tc.expectErr, psHeader.ValidateBasic() != nil, "Validate Basic had an unexpected result")
		})
	}
}

func TestPartValidateBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	testCases := []struct {
		testName     string
		malleatePart func(*Part)
		expectErr    bool
	}{
		{"Good Part", func(pt *Part) {}, false},
		{"Too big part", func(pt *Part) { pt.Bytes = make([]byte, BlockPartSizeBytes+1) }, true},
		{"Too big proof", func(pt *Part) {
			pt.Proof = merkle.Proof{
				Total:    1,
				Index:    1,
				LeafHash: make([]byte, 1024*1024),
			}
		}, true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			data := tmrand.Bytes(testPartSize * 100)
			ps := NewPartSetFromData(data, testPartSize)
			part := ps.GetPart(0)
			tc.malleatePart(part)
			assert.Equal(t, tc.expectErr, part.ValidateBasic() != nil, "Validate Basic had an unexpected result")
		})
	}
}

func TestParSetHeaderProtoBuf(t *testing.T) {
	testCases := []struct {
		msg     string
		ps1     *PartSetHeader
		expPass bool
	}{
		{"success empty", &PartSetHeader{}, true},
		{"success",
			&PartSetHeader{Total: 1, Hash: []byte("hash")}, true},
	}

	for _, tc := range testCases {
		protoBlockID := tc.ps1.ToProto()

		psh, err := PartSetHeaderFromProto(&protoBlockID)
		if tc.expPass {
			require.Equal(t, tc.ps1, psh, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

func TestPartProtoBuf(t *testing.T) {

	proof := merkle.Proof{
		Total:    1,
		Index:    1,
		LeafHash: tmrand.Bytes(32),
	}
	testCases := []struct {
		msg     string
		ps1     *Part
		expPass bool
	}{
		{"failure empty", &Part{}, false},
		{"failure nil", nil, false},
		{"success",
			&Part{Index: 1, Bytes: tmrand.Bytes(32), Proof: proof}, true},
	}

	for _, tc := range testCases {
		proto, err := tc.ps1.ToProto()
		if tc.expPass {
			require.NoError(t, err, tc.msg)
		}

		p, err := PartFromProto(proto)
		if tc.expPass {
			require.NoError(t, err)
			require.Equal(t, tc.ps1, p, tc.msg)
		}
	}
}

func TestPartSetHeaderValidateBasicTotal(t *testing.T) {
	testCases := []struct {
		name      string
		total     uint32
		expectErr bool
	}{
		{"zero", 0, false},
		{"one", 1, false},
		{"max", MaxBlockPartsCount, false},
		{"max plus one", MaxBlockPartsCount + 1, true},
		{"max uint32", math.MaxUint32, true},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			psh := PartSetHeader{Total: tc.total, Hash: tmrand.Bytes(32)}
			err := psh.ValidateBasic()
			assert.Equal(t, tc.expectErr, err != nil, "unexpected validation result: %v", err)
		})
	}
}

func TestProposalPartSetHeaderTotalPropagation(t *testing.T) {
	// An oversized Total inside a Proposal's BlockID.PartSetHeader must be
	// rejected via the Proposal -> BlockID -> PartSetHeader admission chain.
	proposalWithTotal := func(total uint32) *Proposal {
		blockID := BlockID{
			Hash:          tmrand.Bytes(crypto.HashSize),
			PartSetHeader: PartSetHeader{Total: total, Hash: tmrand.Bytes(crypto.HashSize)},
			StateID:       tmrand.Bytes(crypto.HashSize),
		}
		p := NewProposal(1, 0, 0, -1, blockID, tmtime.Now())
		p.Signature = tmrand.Bytes(SignatureSize)
		return p
	}

	err := proposalWithTotal(MaxBlockPartsCount + 1).ValidateBasic()
	require.Error(t, err, "Proposal with oversized PartSetHeader.Total must fail validation")
	require.Contains(t, err.Error(), "too many parts")

	require.NoError(t, proposalWithTotal(MaxBlockPartsCount).ValidateBasic(),
		"Proposal with in-bound PartSetHeader.Total must validate")
}

func TestPartIndexProofBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	data := tmrand.Bytes(testPartSize * 5)
	source := NewPartSetFromData(data, testPartSize)

	// genuine part: passes both ValidateBasic and AddPart
	genuine := source.GetPart(1)
	require.NoError(t, genuine.ValidateBasic())
	dest := NewPartSetFromHeader(source.Header())
	added, err := dest.AddPart(genuine)
	require.True(t, added)
	require.NoError(t, err)

	// part relabeled to another valid slot (proof untouched) is rejected by both
	relabeled := &Part{
		Index: 2,
		Bytes: source.GetPart(1).Bytes,
		Proof: source.GetPart(1).Proof,
	}
	err = relabeled.ValidateBasic()
	require.Error(t, err, "relabeled part must fail ValidateBasic")
	require.ErrorIs(t, err, ErrPartSetIndexMismatch, "ValidateBasic mismatch must wrap ErrPartSetIndexMismatch")
	dest2 := NewPartSetFromHeader(source.Header())
	added, err = dest2.AddPart(relabeled)
	require.False(t, added)
	require.ErrorIs(t, err, ErrPartSetIndexMismatch)

	// after rejecting the relabeled part, the genuine part for that slot is still accepted
	added, err = dest2.AddPart(source.GetPart(2))
	require.True(t, added)
	require.NoError(t, err)

	// relabeled to an out-of-range index still hits the unexpected-index path
	outOfRange := &Part{
		Index: source.Total(),
		Bytes: source.GetPart(0).Bytes,
		Proof: source.GetPart(0).Proof,
	}
	added, err = dest2.AddPart(outOfRange)
	require.False(t, added)
	require.ErrorIs(t, err, ErrPartSetUnexpectedIndex)
}
