package statesync

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/suite"

	clientmocks "github.com/tendermint/tendermint/abci/client/mocks"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
)

type snapshotRepositoryTestSuite struct {
	suite.Suite

	client *clientmocks.Client
	repo   *snapshotRepository
}

func TestSnapshotRepository(t *testing.T) {
	suite.Run(t, new(snapshotRepositoryTestSuite))
}

func (suite *snapshotRepositoryTestSuite) SetupTest() {
	suite.client = clientmocks.NewClient(suite.T())
	suite.repo = newSnapshotRepository(suite.client, log.NewNopLogger())
}

func (suite *snapshotRepositoryTestSuite) TestOfferSnapshot() {
	ctx := context.Background()
	fakeSnapshot := &snapshot{
		Height:  1,
		Version: 0,
		Hash:    []byte{1, 2, 3, 4, 5},
	}
	req := &abci.RequestOfferSnapshot{
		Snapshot: &abci.Snapshot{
			Height:   fakeSnapshot.Height,
			Version:  fakeSnapshot.Version,
			Hash:     fakeSnapshot.Hash,
			Metadata: fakeSnapshot.Metadata,
		},
	}
	testCases := []struct {
		snapshot *snapshot
		wantErr  string
		result   abci.ResponseOfferSnapshot_Result
	}{
		{
			snapshot: fakeSnapshot,
			result:   abci.ResponseOfferSnapshot_ACCEPT,
		},
		{
			snapshot: fakeSnapshot,
			result:   abci.ResponseOfferSnapshot_ABORT,
			wantErr:  errAbort.Error(),
		},
		{
			snapshot: fakeSnapshot,
			result:   abci.ResponseOfferSnapshot_REJECT,
			wantErr:  errRejectSnapshot.Error(),
		},
		{
			snapshot: fakeSnapshot,
			result:   abci.ResponseOfferSnapshot_REJECT_FORMAT,
			wantErr:  errRejectFormat.Error(),
		},
		{
			snapshot: fakeSnapshot,
			result:   abci.ResponseOfferSnapshot_REJECT_SENDER,
			wantErr:  errRejectSender.Error(),
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			suite.client.
				On("OfferSnapshot", ctx, req).
				Once().
				Return(&abci.ResponseOfferSnapshot{Result: tc.result}, nil)
			err := suite.repo.offerSnapshot(ctx, tc.snapshot)
			if tc.wantErr != "" {
				suite.Require().ErrorContains(err, tc.wantErr)
			} else {
				suite.Require().NoError(err)
			}
		})
	}
}

func (suite *snapshotRepositoryTestSuite) TestLoadSnapshotChunk() {
	ctx := context.Background()
	data := []byte{1, 2, 3, 4, 5}
	suite.client.
		On("LoadSnapshotChunk", ctx, &abci.RequestLoadSnapshotChunk{
			Height:  1,
			Version: 2,
			ChunkId: []byte{3},
		}).
		Once().
		Return(&abci.ResponseLoadSnapshotChunk{Chunk: data}, nil)
	resp, err := suite.repo.loadSnapshotChunk(ctx, 1, 2, []byte{3})
	suite.Require().NoError(err)
	suite.Require().Equal(data, resp.Chunk)
}

func (suite *snapshotRepositoryTestSuite) TestRecentSnapshots() {
	ctx := context.Background()
	const storedLen = 20
	storedSnapshots := make([]*abci.Snapshot, 0, storedLen)
	for i := 1; i <= storedLen; i++ {
		storedSnapshots = append(storedSnapshots, &abci.Snapshot{
			Height:  uint64(i * 1000),
			Version: 0,
		})
	}
	rand.Shuffle(20, func(i, j int) {
		storedSnapshots[i], storedSnapshots[j] = storedSnapshots[j], storedSnapshots[i]
	})
	suite.client.
		On("ListSnapshots", ctx, &abci.RequestListSnapshots{}).
		Once().
		Return(&abci.ResponseListSnapshots{Snapshots: storedSnapshots}, nil)
	snapshots, err := suite.repo.recentSnapshots(ctx, 5)
	suite.Require().NoError(err)
	suite.Require().Len(snapshots, 5)
	heights := make([]uint64, 0, 5)
	for _, ss := range snapshots {
		heights = append(heights, ss.Height)
	}
	suite.Require().Equal([]uint64{20000, 19000, 18000, 17000, 16000}, heights)
}
