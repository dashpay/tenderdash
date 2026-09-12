package mockcoreserver

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dashpay/dashd-go/btcjson"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/privval"
	"github.com/dashpay/tenderdash/types"
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
	// Quorums holds complete `quorum info` answers keyed by lower-case hex
	// quorum hash (see QuorumsFromValidatorSetUpdates). A quorum missing here
	// is answered from FilePV, which only knows the local node's own share.
	Quorums map[string]btcjson.QuorumInfoResult
}

// QuorumsFromValidatorSetUpdates builds the `quorum info` answers Dash Core
// would give for every quorum of a validator set schedule: all members with
// their public key shares and the threshold public key. State sync
// authenticates light block validator sets against exactly these fields.
func QuorumsFromValidatorSetUpdates(
	llmqType btcjson.LLMQType,
	updates map[int64]abci.ValidatorSetUpdate,
) (map[string]btcjson.QuorumInfoResult, error) {
	quorums := make(map[string]btcjson.QuorumInfoResult, len(updates))
	for height, update := range updates {
		thresholdKey, err := encoding.PubKeyFromProto(update.ThresholdPublicKey)
		if err != nil {
			return nil, fmt.Errorf("threshold public key of the update at height %d: %w", height, err)
		}
		members := make([]btcjson.QuorumMember, 0, len(update.ValidatorUpdates))
		for _, validator := range update.ValidatorUpdates {
			member := btcjson.QuorumMember{ProTxHash: hex.EncodeToString(validator.ProTxHash), Valid: true}
			if validator.PubKey != nil {
				pubKey, err := encoding.PubKeyFromProto(*validator.PubKey)
				if err != nil {
					return nil, fmt.Errorf("public key of %X at height %d: %w", validator.ProTxHash, height, err)
				}
				member.PubKeyShare = hex.EncodeToString(pubKey.Bytes())
			}
			members = append(members, member)
		}
		quorumHash := hex.EncodeToString(update.QuorumHash)
		quorums[quorumHash] = btcjson.QuorumInfoResult{
			Height:          math.MustConvertUint32(height),
			Type:            llmqType.Name(),
			QuorumHash:      quorumHash,
			Members:         members,
			QuorumPublicKey: hex.EncodeToString(thresholdKey.Bytes()),
		}
	}
	return quorums, nil
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
	if info, ok := c.Quorums[strings.ToLower(*cmd.QuorumHash)]; ok {
		return info
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

	// Dash Core reports the quorum type by its LLMQ name (e.g. "llmq_test"),
	// which is what state sync's validator-set authentication parses.
	return btcjson.QuorumInfoResult{
		Height:          math.MustConvertUint32(height),
		Type:            c.LLMQType.Name(),
		QuorumHash:      quorumHash.String(),
		Members:         members,
		QuorumPublicKey: hex.EncodeToString(tpk.Bytes()),
	}
}

// QuorumSign returns a quorum-sign result
func (c *MockCoreServer) QuorumSign(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumSignResult {
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
	signID := types.NewSignItemFromHash(*cmd.LLMQType, quorumHash, reqID, msgHash).SignHash

	privateKey, err := c.FilePV.GetPrivateKey(ctx, quorumHash)
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
	signID := types.NewSignItemFromHash(*cmd.LLMQType, quorumHash, reqID, msgHash).SignHash

	thresholdPublicKey, err := c.FilePV.GetThresholdPublicKey(ctx, quorumHash)
	if err != nil {
		panic(err)
	}

	signatureVerified := thresholdPublicKey.VerifySignatureDigest(signID, signature)

	res := btcjson.QuorumVerifyResult{Result: signatureVerified}
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
		State:     btcjson.MNStatusStateReady,
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
func (c *StaticCoreServer) Ping(_ context.Context, _cmd btcjson.PingCmd) error {
	return nil
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
