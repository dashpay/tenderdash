package light

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light/provider"
	provider_mocks "github.com/dashpay/tenderdash/light/provider/mocks"
	"github.com/dashpay/tenderdash/types"
)

var errTestBadWitness = errors.New("bad witness")

var testBlockTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// testLightBlock builds a minimal light block whose hash is determined by the
// validatorsHash, which is enough to drive the header-comparison logic. The
// block time is fixed so equal inputs hash equally.
func testLightBlock(height int64, validatorsHash []byte) *types.LightBlock {
	return &types.LightBlock{
		SignedHeader: &types.SignedHeader{
			Header: &types.Header{
				Height:         height,
				Time:           testBlockTime,
				ValidatorsHash: validatorsHash,
			},
		},
	}
}

func mockWitness(lb *types.LightBlock, id string) *provider_mocks.Provider {
	w := &provider_mocks.Provider{}
	w.On("LightBlock", mock.Anything, mock.Anything).Return(lb, nil).Maybe()
	w.On("ID").Return(id).Maybe()
	return w
}

// TestCompareNewHeaderWithWitness_Conflict is a regression test for the missing
// return after a conflicting-header send: a witness reporting a different header
// must produce exactly one message on the channel.
func TestCompareNewHeaderWithWitness_Conflict(t *testing.T) {
	primary := testLightBlock(5, []byte("primary-validators-hash"))
	conflicting := testLightBlock(5, []byte("witness-validators-hash"))
	require.NotEqual(t, primary.Hash(), conflicting.Hash())

	c := &Client{logger: log.NewNopLogger()}
	witness := mockWitness(conflicting, "witness-1")

	// Buffer of two so a (buggy) double send would not block; the assertion is
	// that exactly one message arrives.
	errc := make(chan error, 2)
	c.compareNewHeaderWithWitness(context.Background(), errc, primary.SignedHeader, witness, 0)
	close(errc)

	msgs := make([]error, 0, 2)
	for err := range errc {
		msgs = append(msgs, err)
	}

	require.Len(t, msgs, 1, "exactly one message expected on conflict")
	require.IsType(t, errConflictingHeaders{}, msgs[0])
}

// TestCompareNewHeaderWithWitness_Match confirms a matching witness header
// produces a single nil message.
func TestCompareNewHeaderWithWitness_Match(t *testing.T) {
	header := testLightBlock(5, []byte("shared-validators-hash"))
	matching := testLightBlock(5, []byte("shared-validators-hash"))
	require.Equal(t, header.Hash(), matching.Hash())

	c := &Client{logger: log.NewNopLogger()}
	witness := mockWitness(matching, "witness-1")

	errc := make(chan error, 2)
	c.compareNewHeaderWithWitness(context.Background(), errc, header.SignedHeader, witness, 0)
	close(errc)

	msgs := make([]error, 0, 2)
	for err := range errc {
		msgs = append(msgs, err)
	}

	require.Len(t, msgs, 1, "exactly one message expected on match")
	require.NoError(t, msgs[0])
}

// TestCompareFirstHeaderWithWitnesses covers the witness cross-check flow: a
// conflicting witness fails verification while being kept, a bad witness is
// removed, and full agreement succeeds.
func TestCompareFirstHeaderWithWitnesses(t *testing.T) {
	primary := testLightBlock(5, []byte("primary-validators-hash"))
	matching := func() *types.LightBlock { return testLightBlock(5, []byte("primary-validators-hash")) }
	conflicting := func() *types.LightBlock { return testLightBlock(5, []byte("other-validators-hash")) }

	testCases := []struct {
		name             string
		witnesses        func() []provider.Provider
		wantErr          bool
		wantConflict     bool
		wantWitnessCount int
	}{
		{
			name: "all witnesses agree",
			witnesses: func() []provider.Provider {
				return []provider.Provider{
					mockWitness(matching(), "w0"),
					mockWitness(matching(), "w1"),
				}
			},
			wantErr:          false,
			wantWitnessCount: 2,
		},
		{
			name: "one conflicting witness fails verification but is kept",
			witnesses: func() []provider.Provider {
				return []provider.Provider{
					mockWitness(matching(), "w0"),
					mockWitness(conflicting(), "w1"),
				}
			},
			wantErr:          true,
			wantConflict:     true,
			wantWitnessCount: 2,
		},
		{
			name: "an erroring witness is removed when others agree",
			witnesses: func() []provider.Provider {
				bad := &provider_mocks.Provider{}
				bad.On("LightBlock", mock.Anything, mock.Anything).
					Return(nil, provider.ErrUnreliableProvider{Reason: errTestBadWitness}).Maybe()
				bad.On("ID").Return("bad").Maybe()
				return []provider.Provider{
					mockWitness(matching(), "w0"),
					mockWitness(matching(), "w1"),
					bad,
				}
			},
			wantErr:          false,
			wantWitnessCount: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{logger: log.NewNopLogger(), witnesses: tc.witnesses()}
			err := c.compareFirstHeaderWithWitnesses(context.Background(), primary.SignedHeader)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantConflict {
					require.ErrorIs(t, err, ErrConflictingWitnessHeader)
				}
			} else {
				require.NoError(t, err)
			}
			require.Len(t, c.witnesses, tc.wantWitnessCount)
		})
	}
}
