package consensus

import (
	"context"
	"encoding/hex"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/mock"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
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
	quorumHash := crypto.QuorumHash(mustHexToBytes("C728518B1D147017E776E8808573F137186DC09BBCC4C1D338941F51C85467E1"))
	thPubKey := bls12381.PubKey(mustHexToBytes("878fd2c938e1567b42b04074c12dfdea51513a90075964325b02300973e046ad60e06d3a471b0461dbbd34245ce9e4c9"))
	confs := []struct {
		proTxHash string
		privKey   string
		pubKey    string
	}{
		{
			proTxHash: "54479da949503ba2820d9bb3ef3f5fa240eddd454dc2ed1f0b89e1f235df9e8d",
			privKey:   "6d9d08376b0ca7572daf07005453be3c492bb86a6eed3ca0edcfa3d764e670c4",
			pubKey:    "0eff294db23dd8d2a00d6736d2a76e3b30cef825f12ff60be6d4ae013845eb120a8806a180a198b7357f9c755f2b27c7",
		},
		{
			proTxHash: "f9d5be5068f684a336090e0acb63d874bdb0a4b871d5b2faa92332fd0e2324dc",
			privKey:   "388b9165caf570a9d8e460fdd10783d9e142b33f2af2e131c3e60ed6090d8f41",
			pubKey:    "931489fad6c24e7e9dc14fd6ddc55a6085f68a0b625fce0a94531c62c4b90f8c0f7e5b884827ad98c61cafd29cbd936d",
		},
	}
	privVals := make([]types.PrivValidator, 2)
	valz := make([]*types.Validator, 2)
	for i, conf := range confs {
		proTxHash := mustHexToBytes(conf.proTxHash)
		pubKey := bls12381.PubKey(mustHexToBytes(conf.pubKey))
		valz[i] = types.NewValidator(pubKey, types.DefaultDashVotingPower, proTxHash, "")
		privVals[i] = types.NewMockPVWithParams(
			bls12381.PrivKey(mustHexToBytes(conf.privKey)),
			proTxHash,
			quorumHash,
			thPubKey,
			false,
			false,
		)
	}
	return types.NewValidatorSet(valz, thPubKey, btcjson.LLMQType_5_60, quorumHash, true), privVals
}

func mustHexToBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
