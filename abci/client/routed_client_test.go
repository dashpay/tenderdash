package abciclient_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/client/mocks"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestRouting tests the RoutedClient.
//
// Given 3 clients: defaultApp, consensusApp and queryApp:
// * when a request of type Info is made, it should be routed to defaultApp
// * when a request of type FinalizeBlock is made, it should be first routed to queryApp, then to consensusApp
// * when a request of type CheckTx is made, it should be routed to queryApp
// * when a request of type PrepareProposal is made, it should be routed to to consensusApp
func TestRouting(t *testing.T) {
	ctx := context.TODO()

	defaultApp := mocks.NewClient(t)
	defer defaultApp.AssertExpectations(t)
	defaultApp.On("Info", mock.Anything, mock.Anything).Return(&types.ResponseInfo{
		Data: "info",
	}, nil).Once()

	queryApp := mocks.NewClient(t)
	defer queryApp.AssertExpectations(t)
	queryApp.On("CheckTx", mock.Anything, mock.Anything).Return(&types.ResponseCheckTx{
		Priority: 1,
	}, nil).Once()
	queryApp.On("FinalizeBlock", mock.Anything, mock.Anything).Return(&types.ResponseFinalizeBlock{}, nil).Once()

	consensusApp := mocks.NewClient(t)
	defer consensusApp.AssertExpectations(t)
	consensusApp.On("PrepareProposal", mock.Anything, mock.Anything).Return(&types.ResponsePrepareProposal{
		AppHash: []byte("apphash"),
	}, nil).Once()
	consensusApp.On("FinalizeBlock", mock.Anything, mock.Anything).Return(&types.ResponseFinalizeBlock{
		RetainHeight: 1,
	}, nil).Once()

	logger := log.NewTestingLogger(t)

	routing := abciclient.Routing{
		abciclient.RequestType("CheckTx"):         []abciclient.Client{queryApp},
		abciclient.RequestType("FinalizeBlock"):   []abciclient.Client{queryApp, consensusApp},
		abciclient.RequestType("PrepareProposal"): []abciclient.Client{consensusApp},
	}

	routedClient, err := abciclient.NewRoutedClient(logger, defaultApp, routing)
	assert.NoError(t, err)

	// Test routing

	// Info
	_, err = routedClient.Info(ctx, nil)
	assert.NoError(t, err)

	// CheckTx
	_, err = routedClient.CheckTx(ctx, nil)
	assert.NoError(t, err)

	// FinalizeBlock
	_, err = routedClient.FinalizeBlock(ctx, nil)
	assert.NoError(t, err)

	// PrepareProposal
	_, err = routedClient.PrepareProposal(ctx, nil)
	assert.NoError(t, err)
}
