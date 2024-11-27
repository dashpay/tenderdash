package core

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/types"
)

// MockClient is an implementation of a mock core-server
type MockClient struct {
	chainID  string
	llmqType btcjson.LLMQType
	localPV  types.PrivValidator
	canSign  bool
}

func NewMockClient(chainID string, llmqType btcjson.LLMQType, localPV types.PrivValidator, canSign bool) *MockClient {
	if localPV == nil {
		panic("localPV must be set")
	}
	return &MockClient{
		chainID:  chainID,
		llmqType: llmqType,
		localPV:  localPV,
		canSign:  canSign,
	}
}

// Close closes the underlying connection
func (mc *MockClient) Close() error {
	return nil
}

// Ping sends a ping request to the remote signer
func (mc *MockClient) Ping() error {
	return nil
}

func (mc *MockClient) QuorumInfo(
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) (*btcjson.QuorumInfoResult, error) {
	ctx := context.Background()
	var members []btcjson.QuorumMember
	proTxHash, err := mc.localPV.GetProTxHash(ctx)
	if err != nil {
		panic(err)
	}
	pk, err := mc.localPV.GetPubKey(ctx, quorumHash)
	if err != nil {
		panic(err)
	}
	if pk != nil {
		members = append(members, btcjson.QuorumMember{
			ProTxHash:      proTxHash.String(),
			PubKeyOperator: crypto.CRandHex(96),
			Valid:          true,
			PubKeyShare:    pk.HexString(),
		})
	}
	tpk, err := mc.localPV.GetThresholdPublicKey(ctx, quorumHash)
	if err != nil {
		panic(err)
	}
	height, err := mc.localPV.GetHeight(ctx, quorumHash)
	if err != nil {
		panic(err)
	}
	return &btcjson.QuorumInfoResult{
		Height:          math.MustConvertUint32(height),
		Type:            strconv.Itoa(int(quorumType)),
		QuorumHash:      quorumHash.String(),
		Members:         members,
		QuorumPublicKey: tpk.String(),
	}, nil
}

func (mc *MockClient) MasternodeStatus() (*btcjson.MasternodeStatusResult, error) {
	ctx := context.Background()
	proTxHash, err := mc.localPV.GetProTxHash(ctx)
	if err != nil {
		panic(err)
	}
	return &btcjson.MasternodeStatusResult{
		Outpoint:        "",
		Service:         "",
		ProTxHash:       proTxHash.String(),
		CollateralHash:  "",
		CollateralIndex: 0,
		DMNState:        btcjson.DMNState{},
		State:           "",
		Status:          "",
	}, nil
}

func (mc *MockClient) GetNetworkInfo() (*btcjson.GetNetworkInfoResult, error) {
	return &btcjson.GetNetworkInfoResult{
		Version:         0,
		SubVersion:      "",
		ProtocolVersion: 0,
		LocalServices:   "",
		LocalRelay:      false,
		TimeOffset:      0,
		Connections:     0,
		NetworkActive:   false,
		Networks:        nil,
		RelayFee:        0,
		IncrementalFee:  0,
		LocalAddresses:  nil,
		Warnings:        "",
	}, nil
}

func (mc *MockClient) MasternodeListJSON(_filter string) (map[string]btcjson.MasternodelistResultJSON, error) {
	proTxHash, err := mc.localPV.GetProTxHash(context.Background())
	if err != nil {
		panic(err)
	}
	m := make(map[string]btcjson.MasternodelistResultJSON)
	m[""] = btcjson.MasternodelistResultJSON{
		Address:           "",
		Collateraladdress: "",
		Lastpaidblock:     0,
		Lastpaidtime:      0,
		Owneraddress:      "",
		Payee:             "",
		ProTxHash:         proTxHash.String(),
		Pubkeyoperator:    "",
		Status:            "",
		Votingaddress:     "",
	}

	return m, nil
}

func (mc *MockClient) QuorumSign(
	quorumType btcjson.LLMQType,
	requestID tmbytes.HexBytes,
	messageHash tmbytes.HexBytes,
	quorumHash crypto.QuorumHash,
) (*btcjson.QuorumSignResult, error) {
	if err := quorumType.Validate(); err != nil {
		return nil, err
	}
	if !mc.canSign {
		return nil, errors.New("dash core mock client not set up for signing")
	}
	signID := types.NewSignItemFromHash(quorumType, quorumHash, requestID, messageHash).SignHash

	privateKey, err := mc.localPV.GetPrivateKey(context.Background(), quorumHash)
	if err != nil {
		panic(err)
	}

	sign, err := privateKey.SignDigest(signID)
	if err != nil {
		panic(err)
	}

	res := btcjson.QuorumSignResult{
		LLMQType:   int(quorumType),
		QuorumHash: quorumHash.String(),
		ID:         hex.EncodeToString(requestID),
		MsgHash:    hex.EncodeToString(messageHash),
		SignHash:   hex.EncodeToString(signID),
		Signature:  hex.EncodeToString(sign),
	}
	return &res, nil
}

func (mc *MockClient) QuorumVerify(
	quorumType btcjson.LLMQType,
	requestID tmbytes.HexBytes,
	messageHash tmbytes.HexBytes,
	signature tmbytes.HexBytes,
	quorumHash crypto.QuorumHash,
) (bool, error) {
	if err := quorumType.Validate(); err != nil {
		return false, err
	}

	signID := types.NewSignItemFromHash(quorumType, quorumHash, requestID, messageHash).SignHash

	thresholdPublicKey, err := mc.localPV.GetThresholdPublicKey(context.Background(), quorumHash)
	if err != nil {
		panic(err)
	}

	signatureVerified := thresholdPublicKey.VerifySignatureDigest(signID, signature)

	return signatureVerified, nil
}
