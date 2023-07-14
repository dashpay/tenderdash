package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/dash/core/mocks"
)

func TestWaitForMNReady(t *testing.T) {
	testCases := []struct {
		states  []btcjson.MNStatusState
		wantErr string
	}{
		{
			states: []btcjson.MNStatusState{btcjson.MNStatusStateReady},
		},
		{
			states: []btcjson.MNStatusState{
				btcjson.MNStatusStateWaitingForProtx,
				btcjson.MNStatusStateReady,
			},
		},
		{
			states: []btcjson.MNStatusState{
				btcjson.MNStatusStateWaitingForProtx,
				btcjson.MNStatusStateWaitingForProtx,
				btcjson.MNStatusStateWaitingForProtx,
				btcjson.MNStatusStateReady,
			},
		},
		{
			states:  []btcjson.MNStatusState{btcjson.MNStatusStatePoseBanned},
			wantErr: string(btcjson.MNStatusStatePoseBanned),
		},
		{
			states:  []btcjson.MNStatusState{btcjson.MNStatusStateRemoved},
			wantErr: string(btcjson.MNStatusStateRemoved),
		},
		{
			states:  []btcjson.MNStatusState{btcjson.MNStatusStateOperatorKeyChanged},
			wantErr: string(btcjson.MNStatusStateOperatorKeyChanged),
		},
		{
			states:  []btcjson.MNStatusState{btcjson.MNStatusStateProtxIpChanged},
			wantErr: string(btcjson.MNStatusStateProtxIpChanged),
		},
		{
			states:  []btcjson.MNStatusState{btcjson.MNStatusStateError},
			wantErr: string(btcjson.MNStatusStateError),
		},
		{
			states:  []btcjson.MNStatusState{btcjson.MNStatusStateUnknown},
			wantErr: string(btcjson.MNStatusStateUnknown),
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			client := mocks.NewClient(t)
			for _, state := range tc.states {
				client.
					On("MasternodeStatus").
					Once().
					Return(&btcjson.MasternodeStatusResult{State: state}, nil)
			}
			err := WaitForMNReady(client, 1*time.Millisecond)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWaitForMNReadyError(t *testing.T) {
	err := errors.New("some error")
	client := mocks.NewClient(t)
	client.
		On("MasternodeStatus").
		Once().
		Return(nil, err)
	require.ErrorContains(t, WaitForMNReady(client, 1), err.Error())
}
