package light

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

type headerEqualityTrustedStore struct{ block *types.LightBlock }

type headerEqualityProvider struct{ block *types.LightBlock }

func (p headerEqualityProvider) LightBlock(context.Context, int64) (*types.LightBlock, error) {
	return p.block, nil
}
func (p headerEqualityProvider) ReportEvidence(context.Context, types.Evidence) error { return nil }
func (p headerEqualityProvider) ID() string                                           { return "header-provider" }

func (s headerEqualityTrustedStore) SaveLightBlock(*types.LightBlock) error { return nil }
func (s headerEqualityTrustedStore) DeleteLightBlock(int64) error           { return nil }
func (s headerEqualityTrustedStore) LightBlock(int64) (*types.LightBlock, error) {
	return s.block, nil
}
func (s headerEqualityTrustedStore) LastLightBlockHeight() (int64, error) { return s.block.Height, nil }
func (s headerEqualityTrustedStore) FirstLightBlockHeight() (int64, error) {
	return s.block.Height, nil
}
func (s headerEqualityTrustedStore) LightBlockBefore(int64) (*types.LightBlock, error) {
	return s.block, nil
}
func (s headerEqualityTrustedStore) Prune(uint16) error { return nil }
func (s headerEqualityTrustedStore) Size() uint16       { return 1 }

// VerifyHeader treats Header.Hash as the complete identity of an already
// trusted header, so every consensus-relevant header field must affect it.
func TestVerifyHeaderRejectsChangedCoreChainLockedHeight(t *testing.T) {
	const chainID = "header-equality"
	vals, _ := types.RandValidatorSet(1)
	header := &types.Header{
		Version:               version.Consensus{Block: version.BlockProtocol, App: 1},
		ChainID:               chainID,
		Height:                1,
		CoreChainLockedHeight: 10,
		Time:                  time.Now().Add(-time.Minute),
		ValidatorsHash:        vals.Hash(),
		NextValidatorsHash:    vals.Hash(),
		ProposerProTxHash:     vals.Proposer().ProTxHash,
	}
	commit := &types.Commit{
		Height: 1,
		BlockID: types.BlockID{
			Hash:    header.Hash(),
			StateID: header.StateID().Hash(),
		},
	}
	trusted := &types.LightBlock{
		SignedHeader: &types.SignedHeader{Header: header, Commit: commit},
		ValidatorSet: vals,
	}
	trustedStore := headerEqualityTrustedStore{block: trusted}

	client := &Client{chainID: chainID, trustedStore: trustedStore, logger: log.NewNopLogger()}
	forged := *header
	forged.CoreChainLockedHeight++
	assert.Equal(t, header.Hash(), forged.Hash(), "the v1.7 block hash format is retained for compatibility")
	assert.Error(t, client.VerifyHeader(context.Background(), &forged, time.Now()),
		"trusted-header identity must reject changed consensus metadata even when legacy hashes collide")
}

func TestVerifyHeaderRejectsMetadataDifferentFromPrimary(t *testing.T) {
	const chainID = "header-primary"
	vals, _ := types.RandValidatorSet(1)
	trustedHeader := &types.Header{
		Version:            version.Consensus{Block: version.BlockProtocol, App: 1},
		ChainID:            chainID,
		Height:             1,
		Time:               time.Now().Add(-2 * time.Minute),
		ValidatorsHash:     vals.Hash(),
		NextValidatorsHash: vals.Hash(),
		ProposerProTxHash:  vals.Proposer().ProTxHash,
	}
	primaryHeader := *trustedHeader
	primaryHeader.Height = 2
	primaryHeader.Time = time.Now().Add(-time.Minute)
	primaryHeader.CoreChainLockedHeight = 10
	primaryBlock := &types.LightBlock{
		SignedHeader: &types.SignedHeader{Header: &primaryHeader, Commit: &types.Commit{Height: 2}},
		ValidatorSet: vals,
	}
	trustedBlock := &types.LightBlock{
		SignedHeader: &types.SignedHeader{Header: trustedHeader, Commit: &types.Commit{Height: 1}},
		ValidatorSet: vals,
	}
	client := &Client{
		chainID:            chainID,
		primary:            headerEqualityProvider{block: primaryBlock},
		trustedStore:       headerEqualityTrustedStore{block: trustedBlock},
		latestTrustedBlock: trustedBlock,
		logger:             log.NewNopLogger(),
	}

	forged := primaryHeader
	forged.CoreChainLockedHeight++
	assert.Equal(t, primaryHeader.Hash(), forged.Hash(), "fixture must collide under the legacy hash")
	err := client.VerifyHeader(context.Background(), &forged, time.Now())
	require.ErrorContains(t, err, "header from primary",
		"the requested header must match every field returned by the primary")
}
