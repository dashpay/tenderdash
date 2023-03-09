package client

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/tendermint/tendermint/internal/p2p"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
)

func TestValidateMessageHandler(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		envelope p2p.Envelope
		wantErr  string
	}{
		{
			envelope: p2p.Envelope{ChannelID: 0},
			wantErr:  "unknown channel ID",
		},
		{
			envelope: p2p.Envelope{ChannelID: 1},
			wantErr:  ErrRequestIDAttributeRequired.Error(),
		},
		{
			envelope: p2p.Envelope{
				ChannelID: 1,
				Attributes: map[string]string{
					RequestIDAttribute: uuid.NewString(),
				},
				Message: &bcproto.BlockResponse{},
			},
			wantErr: ErrResponseIDAttributeRequired.Error(),
		},
		{
			envelope: p2p.Envelope{
				ChannelID: 1,
				Attributes: map[string]string{
					RequestIDAttribute: uuid.NewString(),
				},
			},
		},
	}
	fakeHandler := newMockConsumer(t)
	fakeHandler.
		On("Handle", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(nil)
	hd := validateMessageHandler{
		channelID: 1,
		next:      fakeHandler,
	}
	client := &Client{}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			err := hd.Handle(ctx, client, &tc.envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}
