package selectpeers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash/quorum/mock"
	"github.com/dashpay/tenderdash/types"
)

// TestDIP6InboundIsInverseOfOutbound pins the pairing the two selections have to
// satisfy: b is told to connect to a exactly when a expects b to connect to it.
//
// Without it the inbound selection would reserve connection slots for validators
// that never arrive, while the ones that do arrive stay displaceable — a failure
// that is invisible until a node is under a connection flood.
func TestDIP6InboundIsInverseOfOutbound(t *testing.T) {
	quorumHash := mock.NewQuorumHash(0xaa)

	for _, size := range []int{2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 33, 64, 65, 100} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			validators := mock.NewValidators(uint16(size))
			selector := NewDIP6ValidatorSelector(quorumHash)

			outbound := map[string]map[string]bool{}
			inbound := map[string]map[string]bool{}
			for _, me := range validators {
				out, err := selector.SelectValidators(validators, me)
				require.NoError(t, err)
				in, err := selector.SelectInboundValidators(validators, me)
				require.NoError(t, err)

				outbound[me.ProTxHash.String()] = proTxHashSet(out)
				inbound[me.ProTxHash.String()] = proTxHashSet(in)
			}

			for _, a := range validators {
				for _, b := range validators {
					keyA, keyB := a.ProTxHash.String(), b.ProTxHash.String()
					assert.Equal(t, outbound[keyA][keyB], inbound[keyB][keyA],
						"%s connects to %s: outbound says %v, inbound says %v",
						keyA, keyB, outbound[keyA][keyB], inbound[keyB][keyA])
				}
			}

			// A validator never expects a connection from itself.
			for _, me := range validators {
				assert.False(t, inbound[me.ProTxHash.String()][me.ProTxHash.String()])
			}
		})
	}
}

// TestDIP6InboundRejectsNonMembers checks the inbound selection refuses the same
// inputs as the outbound one, so a caller cannot get a set of reservations for a
// quorum it does not belong to.
func TestDIP6InboundRejectsNonMembers(t *testing.T) {
	selector := NewDIP6ValidatorSelector(mock.NewQuorumHash(0xaa))
	outsider := mock.NewValidator(mySeed)

	_, err := selector.SelectInboundValidators(mock.NewValidators(8), outsider)
	assert.Error(t, err)

	_, err = selector.SelectInboundValidators([]*types.Validator{outsider}, outsider)
	assert.Error(t, err)
}

func proTxHashSet(validators []*types.Validator) map[string]bool {
	set := make(map[string]bool, len(validators))
	for _, validator := range validators {
		set[validator.ProTxHash.String()] = true
	}
	return set
}
