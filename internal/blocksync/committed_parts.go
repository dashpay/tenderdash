package blocksync

import (
	"errors"
	"fmt"
	"slices"

	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	tmcrypto "github.com/dashpay/tenderdash/proto/tendermint/crypto"
	"github.com/dashpay/tenderdash/types"
)

// CommittedParts is a block as it was committed. The commit's part set header
// is computed over the parts the proposer gossiped, and a peer can only rebuild
// it from those: a re-encoding of the decoded block drops unknown fields and
// non-canonical encoding, and consensus accepts any parts that verify against
// the header, however the proposer cut the block.
type CommittedParts struct {
	// Bytes are the bytes of the parts, in order.
	Bytes []byte
	// Sizes is the size of each part, nil for parts of types.BlockPartSizeBytes.
	Sizes []uint32
	// Proofs is the Merkle proof of each part. It is nil when the part set header
	// can be computed from Bytes and Sizes.
	Proofs []merkle.Proof
}

// loadCommittedParts returns the block at height as it was committed, or nil
// when the store does not hold every part of it.
func loadCommittedParts(store sm.BlockStore, height int64) *CommittedParts {
	meta := store.LoadBlockMeta(height)
	if meta == nil {
		return nil
	}
	total := int(meta.BlockID.PartSetHeader.Total)
	// BlockSize is -1 for a header saved without its block
	blockBytes := make([]byte, 0, max(meta.BlockSize, 0))
	sizes := make([]uint32, total)
	proofs := make([]merkle.Proof, total)
	computable := true
	for i := range total {
		part := store.LoadBlockPart(height, i)
		// a header saved without its block, or parts pruned after the meta was
		// loaded
		if part == nil {
			return nil
		}
		blockBytes = append(blockBytes, part.Bytes...)
		sizes[i] = tmmath.MustConvertUint32(len(part.Bytes))
		proofs[i] = part.Proof
		// PartSet.AddPart checks a proof against the header's root but not its
		// total, so a proposer can commit to a tree of more leaves than the
		// header has parts. Every part verified at the header's total means the
		// tree over the parts is the header's.
		computable = computable && part.Proof.Total == int64(total)
	}
	if len(blockBytes) == 0 {
		return nil
	}
	parts := &CommittedParts{Bytes: blockBytes, Sizes: sizes, Proofs: proofs}
	if computable {
		parts.Proofs = nil
		if standardPartSizes(sizes, len(blockBytes)) {
			parts.Sizes = nil
		}
	}
	return parts
}

// standardPartSizes reports whether parts of the given sizes are how
// types.NewPartSetFromData cuts size bytes into parts of
// types.BlockPartSizeBytes.
func standardPartSizes(partSizes []uint32, size int) bool {
	partSize := int(types.BlockPartSizeBytes)
	total := (size + partSize - 1) / partSize
	if total == 0 || len(partSizes) != total {
		return false
	}
	// the last part takes what is left, and with total parts that is between 1
	// and partSize bytes
	for _, s := range partSizes[:total-1] {
		if s != types.BlockPartSizeBytes {
			return false
		}
	}
	return true
}

// committedPartsFromProto returns the parts in a block response, after checking
// that they are well formed. It does not check them against the commit.
func committedPartsFromProto(resp *bcproto.BlockResponse) (CommittedParts, error) {
	blockBytes := resp.Block
	// A repeated block field leaves the allocation of its longest occurrence
	// behind the last one, and only the length is charged to the backlog.
	if cap(blockBytes) > 2*len(blockBytes) {
		blockBytes = slices.Clone(blockBytes)
	}
	parts := CommittedParts{Bytes: blockBytes}
	if len(resp.PartLayout) == 0 {
		return parts, nil
	}
	if len(resp.PartLayout) > p2p.BlockPartLayoutMaxBytes {
		return CommittedParts{}, fmt.Errorf("part layout too big: %d bytes, max: %d",
			len(resp.PartLayout), p2p.BlockPartLayoutMaxBytes)
	}
	if err := checkPartLayoutBounds(resp.PartLayout); err != nil {
		return CommittedParts{}, err
	}
	var layout bcproto.BlockPartLayout
	if err := proto.Unmarshal(resp.PartLayout, &layout); err != nil {
		return CommittedParts{}, fmt.Errorf("cannot decode part layout: %w", err)
	}
	parts.Sizes = layout.Sizes
	if len(layout.Sizes) > 0 {
		if err := types.ValidatePartSizes(len(blockBytes), layout.Sizes); err != nil {
			return CommittedParts{}, err
		}
	}
	if len(layout.Proofs) == 0 {
		return parts, nil
	}
	if len(layout.Proofs) != len(layout.Sizes) {
		return CommittedParts{}, fmt.Errorf("%d proofs for %d parts", len(layout.Proofs), len(layout.Sizes))
	}
	parts.Proofs = make([]merkle.Proof, len(layout.Proofs))
	for i := range layout.Proofs {
		proof, err := merkle.ProofFromProto(&layout.Proofs[i])
		if err != nil {
			return CommittedParts{}, fmt.Errorf("proof of part %d: %w", i, err)
		}
		if proof.Index != int64(i) {
			return CommittedParts{}, fmt.Errorf("proof of part %d has index %d", i, proof.Index)
		}
		parts.Proofs[i] = *proof
	}
	return parts, nil
}

// Field numbers of BlockPartLayout and tendermint.crypto.Proof.
const (
	layoutSizesField   protowire.Number = 1
	layoutProofsField  protowire.Number = 2
	proofTotalField    protowire.Number = 1
	proofIndexField    protowire.Number = 2
	proofLeafHashField protowire.Number = 3
	proofAuntsField    protowire.Number = 4
)

// checkPartLayoutBounds refuses a serialized BlockPartLayout with more entries
// than a block can have, without decoding it. Decoding allocates tens of bytes
// for every occurrence of a repeated field, and an occurrence can take two
// bytes, so the limits have to hold before the layout is decoded.
//
// The limits only hold if this walk and the decoder split the bytes into the
// same fields, so it accepts nothing but the fields of a layout, each of them
// well formed. The generated decoder lets a varint in a packed field run past
// the end of that field, and skips unknown fields by rules of its own.
func checkPartLayoutBounds(layout []byte) error {
	sizes, proofs := 0, 0
	return walkFields(layout, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		switch {
		case number == layoutSizesField && wireType == protowire.VarintType:
			sizes++
		case number == layoutSizesField && wireType == protowire.BytesType:
			for len(value) > 0 {
				_, n := protowire.ConsumeVarint(value)
				if n < 0 {
					return protowire.ParseError(n)
				}
				sizes++
				value = value[n:]
			}
		case number == layoutProofsField && wireType == protowire.BytesType:
			proofs++
			if err := checkProofBounds(value); err != nil {
				return fmt.Errorf("proof %d: %w", proofs-1, err)
			}
		default:
			return fmt.Errorf("unexpected field %d of wire type %d", number, wireType)
		}
		if sizes > int(types.MaxBlockPartsCount) {
			return fmt.Errorf("too many parts: more than %d", types.MaxBlockPartsCount)
		}
		if proofs > int(types.MaxBlockPartsCount) {
			return fmt.Errorf("too many proofs: more than %d", types.MaxBlockPartsCount)
		}
		return nil
	})
}

// checkProofBounds is checkPartLayoutBounds for a serialized proof.
func checkProofBounds(proof []byte) error {
	aunts := 0
	return walkFields(proof, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		if wireType == protowire.VarintType && (number == proofTotalField || number == proofIndexField) {
			return nil
		}
		if wireType != protowire.BytesType || (number != proofLeafHashField && number != proofAuntsField) {
			return fmt.Errorf("unexpected field %d of wire type %d", number, wireType)
		}
		if len(value) > crypto.HashSize {
			return fmt.Errorf("hash too long: %d bytes, max: %d", len(value), crypto.HashSize)
		}
		if number == proofAuntsField {
			aunts++
		}
		if aunts > merkle.MaxAunts {
			return fmt.Errorf("too many aunts: more than %d", merkle.MaxAunts)
		}
		return nil
	})
}

// walkFields calls visit for every field of a serialized message, with the
// contents of the field when it is length-delimited.
func walkFields(bz []byte, visit func(protowire.Number, protowire.Type, []byte) error) error {
	for len(bz) > 0 {
		number, wireType, n := protowire.ConsumeTag(bz)
		if n < 0 {
			return protowire.ParseError(n)
		}
		bz = bz[n:]
		n = protowire.ConsumeFieldValue(number, wireType, bz)
		if n < 0 {
			return protowire.ParseError(n)
		}
		value := bz[:n]
		if wireType == protowire.BytesType {
			value, _ = protowire.ConsumeBytes(value)
		}
		if err := visit(number, wireType, value); err != nil {
			return err
		}
		bz = bz[n:]
	}
	return nil
}

// partLayout returns the serialized BlockPartLayout of the parts, nil when the
// parts need none.
func (p CommittedParts) partLayout() ([]byte, error) {
	if len(p.Sizes) == 0 && len(p.Proofs) == 0 {
		return nil, nil
	}
	layout := bcproto.BlockPartLayout{Sizes: p.Sizes}
	if len(p.Proofs) > 0 {
		layout.Proofs = make([]tmcrypto.Proof, len(p.Proofs))
		for i := range p.Proofs {
			layout.Proofs[i] = *p.Proofs[i].ToProto()
		}
	}
	return proto.Marshal(&layout)
}

// partSet returns the part set the block was committed with. header is the
// part set header of the commit; the caller still has to verify that commit.
func (p CommittedParts) partSet(header types.PartSetHeader) (*types.PartSet, error) {
	if len(p.Proofs) == 0 {
		if len(p.Sizes) == 0 {
			return types.NewPartSetFromData(p.Bytes, types.BlockPartSizeBytes), nil
		}
		return types.NewPartSetFromPartSizes(p.Bytes, p.Sizes)
	}
	if len(p.Proofs) != len(p.Sizes) || int64(len(p.Proofs)) != int64(header.Total) {
		return nil, fmt.Errorf("%d proofs and %d part sizes for %d parts", len(p.Proofs), len(p.Sizes), header.Total)
	}
	if err := types.ValidatePartSizes(len(p.Bytes), p.Sizes); err != nil {
		return nil, err
	}
	partSet := types.NewPartSetFromHeader(header)
	offset := 0
	for i, size := range p.Sizes {
		part := &types.Part{
			Index: tmmath.MustConvertUint32(i),
			Bytes: p.Bytes[offset : offset+int(size)],
			Proof: p.Proofs[i],
		}
		offset += int(size)
		if _, err := partSet.AddPart(part); err != nil {
			return nil, fmt.Errorf("part %d: %w", i, err)
		}
	}
	if !partSet.IsComplete() {
		return nil, errors.New("incomplete part set")
	}
	return partSet, nil
}
