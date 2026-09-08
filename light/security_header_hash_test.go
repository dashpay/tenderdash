package light

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

type securityTrustedStore struct{ block *types.LightBlock }

func (s securityTrustedStore) SaveLightBlock(*types.LightBlock) error { return nil }
func (s securityTrustedStore) DeleteLightBlock(int64) error           { return nil }
func (s securityTrustedStore) LightBlock(int64) (*types.LightBlock, error) {
	return s.block, nil
}
func (s securityTrustedStore) LastLightBlockHeight() (int64, error)  { return s.block.Height, nil }
func (s securityTrustedStore) FirstLightBlockHeight() (int64, error) { return s.block.Height, nil }
func (s securityTrustedStore) LightBlockBefore(int64) (*types.LightBlock, error) {
	return s.block, nil
}
func (s securityTrustedStore) Prune(uint16) error { return nil }
func (s securityTrustedStore) Size() uint16       { return 1 }

// VerifyHeader treats Header.Hash as the complete identity of an already
// trusted header, so every consensus-relevant header field must affect it.
func TestSecurityVerifyHeaderRejectsChangedCoreChainLockedHeight(t *testing.T) {
	const chainID = "security-header-hash"
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
	trustedStore := securityTrustedStore{block: trusted}

	client := &Client{chainID: chainID, trustedStore: trustedStore, logger: log.NewNopLogger()}
	forged := *header
	forged.CoreChainLockedHeight++
	assert.NotEqual(t, header.Hash(), forged.Hash(), "changed consensus metadata must change the header hash")
	assert.Error(t, client.VerifyHeader(context.Background(), &forged, time.Now()),
		"hash-only trusted-header check must reject changed consensus metadata")
}
