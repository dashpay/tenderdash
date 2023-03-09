package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/tendermint/tendermint/libs/log"

	"github.com/tendermint/tendermint/internal/p2p"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
)

func TestLoggerP2PMessageHandler(t *testing.T) {
	ctx := context.Background()
	reqID := uuid.NewString()
	fakeHandler := newMockConsumer(t)
	logger := log.NewTestingLogger(t)
	testCases := []struct {
		mockFn  func(hd *mockConsumer, logger *log.TestingLogger)
		wantErr string
	}{
		{
			mockFn: func(hd *mockConsumer, logger *log.TestingLogger) {
				logger.AssertMatch(regexp.MustCompile("failed to handle a message from a p2p client"))
				hd.On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(errors.New("error"))
			},
			wantErr: "error",
		},
		{
			mockFn: func(hd *mockConsumer, logger *log.TestingLogger) {
				hd.On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
			},
		},
	}
	client := &Client{}
	envelope := &p2p.Envelope{
		Attributes: map[string]string{RequestIDAttribute: reqID},
	}
	for i, tc := range testCases {
		mw := &loggerP2PMessageHandler{logger: logger, next: fakeHandler}
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			tc.mockFn(fakeHandler, logger)
			err := mw.Handle(ctx, client, envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

func TestRecoveryP2PMessageHandler(t *testing.T) {
	ctx := context.Background()
	fakeHandler := newMockConsumer(t)
	testCases := []struct {
		mockFn  func(fakeHandler *mockConsumer)
		wantErr string
	}{
		{
			mockFn: func(fakeHandler *mockConsumer) {
				fakeHandler.
					On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Panic("panic")
			},
			wantErr: "panic in processing message",
		},
		{
			mockFn: func(fakeHandler *mockConsumer) {
				fakeHandler.
					On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
			},
		},
	}
	logger := log.NewTestingLogger(t)
	mw := recoveryP2PMessageHandler{logger: logger, next: fakeHandler}
	client := &Client{}
	envelope := &p2p.Envelope{}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			tc.mockFn(fakeHandler)
			err := mw.Handle(ctx, client, envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

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
	mw := validateMessageHandler{channelID: 1, next: fakeHandler}
	client := &Client{}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			err := mw.Handle(ctx, client, &tc.envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}
