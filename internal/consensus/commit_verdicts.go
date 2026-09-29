package consensus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"

	"github.com/dashpay/tenderdash/types"
)

// maxCommitVerdicts bounds commitVerdicts. Every entry needs an authenticated
// commit, so the bound matters only under a flood of distinct vectors.
const maxCommitVerdicts = 64

// commitKey identifies a commit by what the application's extension check
// depends on: height, round, block and extension vector. Commits with equal keys
// differ at most in their threshold block signature.
type commitKey struct {
	height     int64
	round      int32
	blockID    string
	extensions [sha256.Size]byte
}

func newCommitKey(commit *types.Commit) commitKey {
	var buf []byte
	for _, ext := range commit.ThresholdVoteExtensions {
		bz, err := ext.Marshal()
		if err != nil {
			panic(fmt.Errorf("marshal commit vote extension: %w", err))
		}
		buf = binary.AppendUvarint(buf, uint64(len(bz)))
		buf = append(buf, bz...)
	}
	return commitKey{
		height:     commit.Height,
		round:      commit.Round,
		blockID:    commit.BlockID.Key(),
		extensions: sha256.Sum256(buf),
	}
}

// commitVerdicts remembers the application's verdicts on commit extension
// vectors at one height, so a commit already judged is neither processed nor
// sent to the application again. It holds verdicts only, never a failure to
// reach the application; the oldest entry goes first once it is full.
//
// Only the consensus goroutine touches it, like commitCandidates, so it needs
// no lock of its own.
type commitVerdicts struct {
	height   int64
	order    []commitKey
	accepted map[commitKey]bool
}

// lookup returns the recorded verdict for key; known is false if there is none.
func (v *commitVerdicts) lookup(key commitKey) (accepted, known bool) {
	if key.height != v.height {
		return false, false
	}
	accepted, known = v.accepted[key]
	return accepted, known
}

// record stores the application's verdict for key, dropping every verdict of
// another height first.
func (v *commitVerdicts) record(key commitKey, accepted bool) {
	if key.height != v.height || v.accepted == nil {
		v.height = key.height
		v.order = v.order[:0]
		v.accepted = make(map[commitKey]bool, maxCommitVerdicts)
	}
	if _, ok := v.accepted[key]; !ok {
		if len(v.order) == maxCommitVerdicts {
			delete(v.accepted, v.order[0])
			v.order = slices.Delete(v.order, 0, 1)
		}
		v.order = append(v.order, key)
	}
	v.accepted[key] = accepted
}
