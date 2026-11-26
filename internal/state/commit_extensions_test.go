package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestVerifyCommitExtensionsABCI(t *testing.T) {
	for _, tc := range []struct {
		name          string
		empty, mutate bool
		status        abci.ResponseVerifyVoteExtension_VerifyStatus
		appErr        error
	}{
		{name: "accept", status: abci.ResponseVerifyVoteExtension_ACCEPT},
		{name: "empty", empty: true, status: abci.ResponseVerifyVoteExtension_ACCEPT},
		{name: "reject", status: abci.ResponseVerifyVoteExtension_REJECT},
		{name: "unknown", status: abci.ResponseVerifyVoteExtension_UNKNOWN},
		{name: "mutation isolation", mutate: true, status: abci.ResponseVerifyVoteExtension_ACCEPT},
		{name: "transport failure remains fatal", appErr: errors.New("connection lost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commit := &types.Commit{Height: 23, Round: 4, BlockID: types.BlockID{Hash: crypto.Checksum([]byte("block"))},
				ThresholdVoteExtensions: tmproto.VoteExtensions{
					{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER, Extension: []byte("contextual")},
					{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, Extension: crypto.Checksum([]byte("withdrawal")),
						XSignRequestId: &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("request")}},
				}}
			if tc.empty {
				commit.ThresholdVoteExtensions = nil
			}
			original := proto.Clone(commit.ToProto())
			calls := 0
			app := &commitExtensionApp{verify: func(req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
				calls++
				require.Empty(t, req.ValidatorProTxHash)
				require.Equal(t, []byte(commit.BlockID.Hash), req.Hash)
				require.Equal(t, commit.Height, req.Height)
				require.Equal(t, commit.Round, req.Round)
				require.Len(t, req.VoteExtensions, len(commit.ThresholdVoteExtensions))
				for i, ext := range req.VoteExtensions {
					require.Equal(t, commit.ThresholdVoteExtensions[i].Type, ext.Type)
					require.Equal(t, commit.ThresholdVoteExtensions[i].Extension, ext.Extension)
					require.Equal(t, commit.ThresholdVoteExtensions[i].GetSignRequestId(), ext.GetSignRequestId())
				}
				if tc.mutate {
					req.Hash[0] ^= 0xff
					req.VoteExtensions[0].Extension[0] ^= 0xff
					req.VoteExtensions[1].GetSignRequestId()[0] ^= 0xff
				}
				return &abci.ResponseVerifyVoteExtension{Status: tc.status}, tc.appErr
			}}
			executor := sm.NewBlockExecutor(nil, abciclient.NewLocalClient(log.NewNopLogger(), app), nil, nil, nil, nil)
			if tc.appErr != nil {
				require.Panics(t, func() { _ = sm.VerifyCommitExtensions(context.Background(), executor, commit) })
			} else if tc.status == abci.ResponseVerifyVoteExtension_ACCEPT {
				require.NoError(t, sm.VerifyCommitExtensions(context.Background(), executor, commit))
			} else {
				err := sm.VerifyCommitExtensions(context.Background(), executor, commit)
				require.ErrorIs(t, err, sm.ErrCommitExtensionsRejected)
				require.ErrorContains(t, err, fmt.Sprintf("height %d round %d block %X", commit.Height, commit.Round, commit.BlockID.Hash))
			}
			require.Equal(t, 1, calls)
			require.True(t, proto.Equal(original, commit.ToProto()), "ABCI request mutation must not alter the verified commit")
		})
	}
}

type commitExtensionApp struct {
	abci.BaseApplication
	verify func(*abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error)
}

func (a *commitExtensionApp) VerifyVoteExtension(_ context.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	return a.verify(req)
}
