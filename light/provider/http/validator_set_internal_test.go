package http

import (
	"context"
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/light/provider"
	rpcmock "github.com/dashpay/tenderdash/rpc/client/mocks"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

// timeoutErr is a *url.Error reporting a timeout, matching the type the
// validatorSet retry path inspects.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func urlTimeout() error {
	return &url.Error{Op: "Get", URL: "http://example", Err: net.Error(timeoutErr{})}
}

// validatorSetProvider exposes the unexported validatorSet method for tests.
func validatorSetProvider(t *testing.T, c *rpcmock.RemoteClient) *http {
	t.Helper()
	p := NewWithClientAndOptions("chain-test", c, Options{MaxRetryAttempts: 3}).(*http)
	return p
}

// page returns a single page of the validator set as a ResultValidators,
// optionally including the threshold/quorum info.
func page(vs *types.ValidatorSet, vals []*types.Validator, withQuorumInfo bool) *coretypes.ResultValidators {
	res := &coretypes.ResultValidators{
		Validators: vals,
		Count:      len(vals),
		Total:      len(vs.Validators),
		QuorumType: vs.QuorumType,
	}
	if withQuorumInfo {
		tpk := vs.ThresholdPublicKey
		qh := vs.QuorumHash
		res.ThresholdPublicKey = &tpk
		res.QuorumHash = &qh
	}
	return res
}

func TestValidatorSetThresholdKey(t *testing.T) {
	const height = int64(5)

	// A real validator set provides a valid threshold key, quorum hash and
	// validators that pass ValidateBasic.
	vs, _ := types.RandValidatorSet(2)
	proposer := vs.Validators[0].ProTxHash

	t.Run("missing threshold public key returns bad light block", func(t *testing.T) {
		c := &rpcmock.RemoteClient{}
		res := page(vs, vs.Validators, true)
		res.ThresholdPublicKey = nil // provider omitted the threshold key
		c.On("Validators", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(res, nil)

		p := validatorSetProvider(t, c)
		vset, err := p.validatorSet(context.Background(), heightPtr(height), proposer)
		require.Nil(t, vset)
		require.Error(t, err)
		require.IsType(t, provider.ErrBadLightBlock{}, err)
	})

	t.Run("missing quorum hash returns bad light block", func(t *testing.T) {
		c := &rpcmock.RemoteClient{}
		res := page(vs, vs.Validators, true)
		res.QuorumHash = nil // provider omitted the quorum hash
		c.On("Validators", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(res, nil)

		p := validatorSetProvider(t, c)
		vset, err := p.validatorSet(context.Background(), heightPtr(height), proposer)
		require.Nil(t, vset)
		require.Error(t, err)
		require.IsType(t, provider.ErrBadLightBlock{}, err)
	})

	t.Run("timeout on first attempt then retry succeeds populates threshold key", func(t *testing.T) {
		c := &rpcmock.RemoteClient{}
		var calls int
		c.On("Validators", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(func(_ context.Context, _ *int64, _ *int, _ *int, requestQuorumInfo *bool) (*coretypes.ResultValidators, error) {
				calls++
				// The first attempt requests the quorum info and times out; the
				// retry must request it again so the threshold key is populated.
				require.True(t, *requestQuorumInfo, "threshold key must be re-requested after a timeout")
				if calls == 1 {
					return nil, urlTimeout()
				}
				return page(vs, vs.Validators, true), nil
			})

		p := validatorSetProvider(t, c)
		vset, err := p.validatorSet(context.Background(), heightPtr(height), proposer)
		require.NoError(t, err)
		require.NotNil(t, vset)
		require.True(t, vset.ThresholdPublicKey.Equals(vs.ThresholdPublicKey))
	})

	t.Run("multi-page happy path advances pages and terminates", func(t *testing.T) {
		// total=2 with perPage>=2 fits on one page, so to exercise the page
		// increment we report a total larger than each page and serve the set
		// across two pages.
		c := &rpcmock.RemoteClient{}
		firstPage := []*types.Validator{vs.Validators[0]}
		secondPage := []*types.Validator{vs.Validators[1]}
		c.On("Validators", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(func(_ context.Context, _ *int64, pg *int, _ *int, requestQuorumInfo *bool) (*coretypes.ResultValidators, error) {
				switch *pg {
				case 1:
					require.True(t, *requestQuorumInfo, "quorum info requested on first page")
					return page(vs, firstPage, true), nil
				case 2:
					require.False(t, *requestQuorumInfo, "quorum info not re-requested after receipt")
					return page(vs, secondPage, false), nil
				default:
					t.Fatalf("validatorSet requested unexpected page %d (page increment missing?)", *pg)
					return nil, nil
				}
			})

		p := validatorSetProvider(t, c)
		vset, err := p.validatorSet(context.Background(), heightPtr(height), proposer)
		require.NoError(t, err)
		require.NotNil(t, vset)
		require.Len(t, vset.Validators, 2)
		require.True(t, vset.ThresholdPublicKey.Equals(vs.ThresholdPublicKey))
	})
}

func heightPtr(h int64) *int64 { return &h }
