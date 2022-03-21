package mockcoreserver

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/dashevo/dashd-go/btcjson"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/privval"
)

// CoreServer is an interface of a mock core-server
type CoreServer interface {
	QuorumInfo(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumInfoResult
	QuorumSign(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumSignResult
	QuorumVerify(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumVerifyResult
	MasternodeStatus(ctx context.Context, cmd btcjson.MasternodeCmd) btcjson.MasternodeStatusResult
	GetNetworkInfo(ctx context.Context, cmd btcjson.GetNetworkInfoCmd) btcjson.GetNetworkInfoResult
	Ping(ctx context.Context, cmd btcjson.PingCmd) error
}

// MockCoreServer is an implementation of a mock core-server
type MockCoreServer struct {
	ChainID  string
	LLMQType btcjson.LLMQType
	FilePV   *privval.FilePV
}

// QuorumInfo returns a quorum-info result
func (c *MockCoreServer) QuorumInfo(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumInfoResult {
	var members []btcjson.QuorumMember
	proTxHash, err := c.FilePV.GetProTxHash(ctx)
	if err != nil {
		panic(err)
	}
	if cmd.QuorumHash == nil {
		err = fmt.Errorf("quorum hash can not be nil when trying to get quorum info")
		panic(err)
	}
	quorumHashBytes, err := hex.DecodeString(*cmd.QuorumHash)
	if len(quorumHashBytes) != crypto.DefaultHashSize {
		err = fmt.Errorf("quorum hash %v is incorrect when trying to get quorum info", *cmd.QuorumHash)
		panic(err)
	}
	quorumHash := crypto.QuorumHash(quorumHashBytes)
	if err != nil {
		panic(err)
	}
	pk, _ := c.FilePV.GetPubKey(ctx, quorumHash)
	// if the public key isn't found that means the node is not part of the quorum, don't add it as a member
	if pk != nil {
		members = append(members, btcjson.QuorumMember{
			ProTxHash:      proTxHash.String(),
			PubKeyOperator: crypto.CRandHex(96),
			Valid:          true,
			PubKeyShare:    pk.HexString(),
		})
	}
	tpk, err := c.FilePV.GetThresholdPublicKey(ctx, quorumHash)
	if err != nil {
		panic(err)
	}
	height, err := c.FilePV.GetHeight(ctx, quorumHash)
	if err != nil {
		panic(err)
	}
	return btcjson.QuorumInfoResult{
		Height:          uint32(height),
		Type:            strconv.Itoa(int(c.LLMQType)),
		QuorumHash:      quorumHash.String(),
		Members:         members,
		QuorumPublicKey: tpk.String(),
	}
}

// QuorumSign returns a quorum-sign result
func (c *MockCoreServer) QuorumSign(_ context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumSignResult {
	reqID, err := hex.DecodeString(strVal(cmd.RequestID))
	if err != nil {
		panic(err)
	}
	msgHash, err := hex.DecodeString(strVal(cmd.MessageHash))
	if err != nil {
		panic(err)
	}

	quorumHashBytes, err := hex.DecodeString(*cmd.QuorumHash)
	if err != nil {
		panic(err)
	}
	quorumHash := crypto.QuorumHash(quorumHashBytes)

	signID := crypto.SignID(
		*cmd.LLMQType,
		bls12381.ReverseBytes(quorumHash),
		bls12381.ReverseBytes(reqID),
		bls12381.ReverseBytes(msgHash),
	)
	privateKey, err := c.FilePV.GetPrivateKey(context.TODO(), quorumHash)
	if err != nil {
		panic(err)
	}

	sign, err := privateKey.SignDigest(signID)
	if err != nil {
		panic(err)
	}

	res := btcjson.QuorumSignResult{
		LLMQType:   int(c.LLMQType),
		QuorumHash: quorumHash.String(),
		ID:         hex.EncodeToString(reqID),
		MsgHash:    hex.EncodeToString(msgHash),
		SignHash:   hex.EncodeToString(signID),
		Signature:  hex.EncodeToString(sign),
	}
	return res
}

// QuorumVerify returns a quorum-verify result
func (c *MockCoreServer) QuorumVerify(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumVerifyResult {
	reqID, err := hex.DecodeString(strVal(cmd.RequestID))
	if err != nil {
		panic(err)
	}
	msgHash, err := hex.DecodeString(strVal(cmd.MessageHash))
	if err != nil {
		panic(err)
	}

	quorumHashBytes, err := hex.DecodeString(*cmd.QuorumHash)
	if err != nil {
		panic(err)
	}
	quorumHash := crypto.QuorumHash(quorumHashBytes)

	signature, err := hex.DecodeString(*cmd.Signature)
	if err != nil {
		panic(err)
	}

	signID := crypto.SignID(
		*cmd.LLMQType,
		bls12381.ReverseBytes(quorumHash),
		bls12381.ReverseBytes(reqID),
		bls12381.ReverseBytes(msgHash),
	)
	thresholdPublicKey, err := c.FilePV.GetThresholdPublicKey(ctx, quorumHash)
	if err != nil {
		panic(err)
	}

	signatureVerified := thresholdPublicKey.VerifySignatureDigest(signID, signature)

	res := btcjson.QuorumVerifyResult{
		Result: signatureVerified,
	}
	return res
}

// MasternodeStatus returns a masternode-status result
func (c *MockCoreServer) MasternodeStatus(ctx context.Context, _ btcjson.MasternodeCmd) btcjson.MasternodeStatusResult {
	proTxHash, err := c.FilePV.GetProTxHash(ctx)
	if err != nil {
		panic(err)
	}
	return btcjson.MasternodeStatusResult{
		ProTxHash: proTxHash.String(),
	}
}

// GetNetworkInfo returns network-info result
func (c *MockCoreServer) GetNetworkInfo(_ context.Context, _ btcjson.GetNetworkInfoCmd) btcjson.GetNetworkInfoResult {
	return btcjson.GetNetworkInfoResult{}
}

// Ping ...
func (c *MockCoreServer) Ping(_ context.Context, _ btcjson.PingCmd) error {
	return nil
}

// StaticCoreServer is a mock of core-server with static result data
type StaticCoreServer struct {
	QuorumInfoResult       btcjson.QuorumInfoResult
	QuorumSignResult       btcjson.QuorumSignResult
	QuorumVerifyResult     btcjson.QuorumVerifyResult
	MasternodeStatusResult btcjson.MasternodeStatusResult
	GetNetworkInfoResult   btcjson.GetNetworkInfoResult
}

// QuorumInfo returns constant quorum-info result
func (c *StaticCoreServer) QuorumInfo(_ context.Context, _ btcjson.QuorumCmd) btcjson.QuorumInfoResult {
	return c.QuorumInfoResult
}

// QuorumSign returns constant quorum-sign result
func (c *StaticCoreServer) QuorumSign(_ context.Context, _ btcjson.QuorumCmd) btcjson.QuorumSignResult {
	return c.QuorumSignResult
}

// QuorumVerify returns constant quorum-sign result
func (c *StaticCoreServer) QuorumVerify(_ context.Context, _ btcjson.QuorumCmd) btcjson.QuorumVerifyResult {
	return c.QuorumVerifyResult
}

// MasternodeStatus returns constant masternode-status result
func (c *StaticCoreServer) MasternodeStatus(_ context.Context, _ btcjson.MasternodeCmd) btcjson.MasternodeStatusResult {
	return c.MasternodeStatusResult
}

// GetNetworkInfo returns constant network-info result
func (c *StaticCoreServer) GetNetworkInfo(_ context.Context, _ btcjson.GetNetworkInfoCmd) btcjson.GetNetworkInfoResult {
	return c.GetNetworkInfoResult
}

// Ping ...
func (c *StaticCoreServer) Ping(_ context.Context, cmd btcjson.PingCmd) error {
	return nil
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
