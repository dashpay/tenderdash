package blocksync

import (
	"errors"
	"fmt"

	"github.com/dashpay/tenderdash/crypto/merkle"
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
	parts := CommittedParts{Bytes: resp.Block, Sizes: resp.PartSizes}
	if len(resp.PartSizes) > 0 {
		if err := types.ValidatePartSizes(len(resp.Block), resp.PartSizes); err != nil {
			return CommittedParts{}, err
		}
	}
	if len(resp.PartProofs) == 0 {
		return parts, nil
	}
	if len(resp.PartProofs) != len(resp.PartSizes) {
		return CommittedParts{}, fmt.Errorf("%d proofs for %d parts", len(resp.PartProofs), len(resp.PartSizes))
	}
	parts.Proofs = make([]merkle.Proof, len(resp.PartProofs))
	for i := range resp.PartProofs {
		proof, err := merkle.ProofFromProto(&resp.PartProofs[i])
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

// committedPartsSize returns the serialized size of the parts in a block
// response.
func committedPartsSize(resp *bcproto.BlockResponse) int {
	size := len(resp.Block) + 4*len(resp.PartSizes)
	for i := range resp.PartProofs {
		size += resp.PartProofs[i].Size()
	}
	return size
}

// protoProofs returns Proofs in their protobuf form.
func (p CommittedParts) protoProofs() []tmcrypto.Proof {
	if p.Proofs == nil {
		return nil
	}
	proofs := make([]tmcrypto.Proof, len(p.Proofs))
	for i := range p.Proofs {
		proofs[i] = *p.Proofs[i].ToProto()
	}
	return proofs
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
