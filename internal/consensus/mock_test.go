package consensus

import (
	"context"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/mock"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

type mockConstructorTesting interface {
	mock.TestingT
	Cleanup(func())
}

type mockWAL struct {
	WAL
	mock.Mock
}

func newMockWAL(t mockConstructorTesting) *mockWAL {
	wal := &mockWAL{}
	wal.Mock.Test(t)
	t.Cleanup(func() { wal.AssertExpectations(t) })
	return wal
}

func (m *mockWAL) FlushAndSync() error {
	ret := m.Called()
	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}
	return r0
}

type mockQueueSender struct {
	mock.Mock
}

func newMockQueueSender(t mockConstructorTesting) *mockQueueSender {
	sender := &mockQueueSender{}
	sender.Mock.Test(t)
	t.Cleanup(func() { sender.AssertExpectations(t) })
	return sender
}

func (m *mockQueueSender) send(ctx context.Context, msg Message, peerID types.NodeID) error {
	ret := m.Called(ctx, msg, peerID)
	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, Message, types.NodeID) error); ok {
		r0 = rf(ctx, msg, peerID)
	} else {
		r0 = ret.Error(0)
	}
	return r0
}

// mockValidatorSet returns static validator set with 2 validators and 2 private keys
func mockValidatorSet() (*types.ValidatorSet, []types.PrivValidator) {
	quorumHash := crypto.QuorumHash(tmbytes.MustHexDecode("D6711FA18C7DA6D3FF8615D3CD3C14500EE91DA5FA942425B8E2B79A30FD8E6C"))
	thPubKey := bls12381.PubKey(tmbytes.MustHexDecode("8E948D00D16B2005B6F6DCB2E37E3C49B116A61BB4F3D899122A4442D94E8AE46757BCE1435586C43B62594D8EDA411E"))
	confs := []struct {
		proTxHash string
		privKey   string
		pubKey    string
	}{
		{
			proTxHash: "56A2659B5B9A7720193D308BEB34FA633B3130F1BA6E5D4090C34DB8DFBA3A73",
			privKey:   "17456894513d6e992020fef2ecc5df71afb369925cb6def571b437d26015f8d7",
			pubKey:    "8280cb6694f181db486c59dfa0c6d12d1c4ca26789340aebad0540ffe2edeac387aceec979454c2cfbe75fd8cf04d56d",
		},
		{
			proTxHash: "7E6C7056A4CE2A26843C9DEC9509E55D97D0C4E29D0936E11A374081BE947A47",
			privKey:   "1f286b5e869825b0e55b09bbd062d1c599aeec68e49e9c773f9ba4f81e315262",
			pubKey:    "b2d09bf043beb595f5856c891e507fcbf4467c9d0d753c29c4ccb75dee18d87c1fbd9b479a4373cc0aa02faebd83b6d7",
		},
	}
	privVals := make([]types.PrivValidator, 2)
	valz := make([]*types.Validator, 2)
	for i, conf := range confs {
		proTxHash := tmbytes.MustHexDecode(conf.proTxHash)
		pubKey := bls12381.PubKey(tmbytes.MustHexDecode(conf.pubKey))
		valz[i] = types.NewValidator(pubKey, types.DefaultDashVotingPower, proTxHash, "")
		privVals[i] = types.NewMockPVWithParams(
			bls12381.PrivKey(tmbytes.MustHexDecode(conf.privKey)),
			proTxHash,
			quorumHash,
			thPubKey,
			false,
			false,
		)
	}
	return types.NewValidatorSet(valz, thPubKey, btcjson.LLMQType_5_60, quorumHash, true), privVals
}
