package privval

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// DashPrivValidator is a PrivValidator that uses Dash-specific logic
type DashPrivValidator interface {
	types.PrivValidator
	dashcore.QuorumVerifier
	DashRPCClient() dashcore.Client
	// QuorumSign executes quorum signature process and returns signature and signHash
	QuorumSign(
		ctx context.Context,
		msgHash []byte,
		requestIDHash []byte,
		quorumType btcjson.LLMQType,
		quorumHash crypto.QuorumHash,
	) (signature []byte, signHash []byte, err error)
}

// DashCoreSignerClient implements DashPrivValidator.
// Handles remote validator connections that provide signing services
type DashCoreSignerClient struct {
	logger            log.Logger
	dashCoreRPCClient dashcore.Client
	cachedProTxHash   crypto.ProTxHash
	defaultQuorumType btcjson.LLMQType
}

var _ DashPrivValidator = (*DashCoreSignerClient)(nil)

// NewDashCoreSignerClient returns an instance of SignerClient.
// it will start the endpoint (if not already started)
func NewDashCoreSignerClient(
	client dashcore.Client,
	defaultQuorumType btcjson.LLMQType,
	logger log.Logger,
) (*DashCoreSignerClient, error) {
	if err := defaultQuorumType.Validate(); err != nil {
		return nil, err
	}
	return &DashCoreSignerClient{
		logger:            logger,
		dashCoreRPCClient: client,
		defaultQuorumType: defaultQuorumType,
	}, nil
}

// Close closes the underlying connection
func (sc *DashCoreSignerClient) Close() error {
	err := sc.dashCoreRPCClient.Close()
	if err != nil {
		return err
	}
	return nil
}

//--------------------------------------------------------
// Implement PrivValidator

// Ping sends a ping request to the remote signer and will retry 2 extra times if failure
func (sc *DashCoreSignerClient) Ping() error {
	var err error
	for i := 0; i < 3; i++ {
		if err = sc.ping(); err == nil {
			return nil
		}
	}

	return err
}

// ping sends a ping request to the remote signer
func (sc *DashCoreSignerClient) ping() error {
	err := sc.dashCoreRPCClient.Ping()
	if err != nil {
		return err
	}

	return nil
}

func (sc *DashCoreSignerClient) ExtractIntoValidator(ctx context.Context, quorumHash crypto.QuorumHash) *types.Validator {
	pubKey, _ := sc.GetPubKey(ctx, quorumHash)
	proTxHash, _ := sc.GetProTxHash(ctx)
	if len(proTxHash) != crypto.DefaultHashSize {
		panic("proTxHash wrong length")
	}
	return &types.Validator{
		PubKey:      pubKey,
		VotingPower: types.DefaultDashVotingPower,
		ProTxHash:   proTxHash,
	}
}

// GetPubKey retrieves a public key from a remote signer
// returns an error if client is not able to provide the key
func (sc *DashCoreSignerClient) GetPubKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	if len(quorumHash.Bytes()) != crypto.DefaultHashSize {
		return nil, fmt.Errorf("quorum hash must be 32 bytes long if requesting public key from dash core")
	}

	response, err := sc.dashCoreRPCClient.QuorumInfo(sc.defaultQuorumType, quorumHash)
	if err != nil {
		return nil, fmt.Errorf("getPubKey Quorum Info Error for (%d) %s : %w", sc.defaultQuorumType, quorumHash.String(), err)
	}

	proTxHash, err := sc.GetProTxHash(ctx)

	if err != nil {
		return nil, fmt.Errorf("getPubKey proTxHash error: %w", err)
	}

	var decodedPublicKeyShare []byte

	found := false

	for _, quorumMember := range response.Members {
		decodedMemberProTxHash, err := hex.DecodeString(quorumMember.ProTxHash)
		if err != nil {
			return nil, fmt.Errorf("error decoding proTxHash : %v", err)
		}
		if len(decodedMemberProTxHash) != crypto.DefaultHashSize {
			return nil, fmt.Errorf(
				"decoding proTxHash %d is incorrect size when getting public key : %v",
				len(decodedMemberProTxHash),
				err,
			)
		}
		if bytes.Equal(proTxHash, decodedMemberProTxHash) {
			decodedPublicKeyShare, err = hex.DecodeString(quorumMember.PubKeyShare)
			found = true
			if err != nil {
				return nil, fmt.Errorf("error decoding publicKeyShare : %v", err)
			}
			if len(decodedPublicKeyShare) != bls12381.PubKeySize {
				return nil, fmt.Errorf(
					"decoding public key share %d is incorrect size when getting public key : %v",
					len(decodedMemberProTxHash),
					err,
				)
			}
			break
		}
	}

	if len(decodedPublicKeyShare) != bls12381.PubKeySize {
		if found {
			// We found it, we should have a public key share
			return nil, fmt.Errorf("no public key share found")
		}
		// We are not part of the quorum, there is no error
		return nil, nil
	}

	return bls12381.PubKey(decodedPublicKeyShare), nil
}

func (sc *DashCoreSignerClient) GetFirstQuorumHash(_ctx context.Context) (crypto.QuorumHash, error) {
	return nil, errors.New("getFirstQuorumHash should not be called on a dash core signer client")
}

func (sc *DashCoreSignerClient) GetThresholdPublicKey(_ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	if len(quorumHash.Bytes()) != crypto.DefaultHashSize {
		return nil, fmt.Errorf("quorum hash must be 32 bytes long if requesting public key from dash core")
	}

	response, err := sc.dashCoreRPCClient.QuorumInfo(sc.defaultQuorumType, quorumHash)
	if err != nil {
		return nil, fmt.Errorf(
			"getThresholdPublicKey Quorum Info Error for (%d) %s : %w",
			sc.defaultQuorumType,
			quorumHash.String(),
			err,
		)
	}
	decodedThresholdPublicKey, err := hex.DecodeString(response.QuorumPublicKey)
	if len(decodedThresholdPublicKey) != bls12381.PubKeySize {
		return nil, fmt.Errorf(
			"decoding thresholdPublicKey %d is incorrect size when getting public key : %v",
			len(decodedThresholdPublicKey),
			err,
		)
	}
	return bls12381.PubKey(decodedThresholdPublicKey), nil
}

func (sc *DashCoreSignerClient) GetHeight(_ctx context.Context, quorumHash crypto.QuorumHash) (int64, error) {
	return 0, fmt.Errorf("getHeight should not be called on a dash core signer client %s", quorumHash.String())
}

func (sc *DashCoreSignerClient) GetProTxHash(_ctx context.Context) (crypto.ProTxHash, error) {
	if sc.cachedProTxHash != nil {
		return sc.cachedProTxHash, nil
	}

	masternodeStatus, err := sc.dashCoreRPCClient.MasternodeStatus()
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	decodedProTxHash, err := hex.DecodeString(masternodeStatus.ProTxHash)
	if err != nil {
		return nil, fmt.Errorf("error decoding proTxHash : %v", err)
	}
	if len(decodedProTxHash) != crypto.DefaultHashSize {
		// We are proof of service banned. Get the proTxHash from our IP Address
		networkInfo, err := sc.dashCoreRPCClient.GetNetworkInfo()
		if err == nil && len(networkInfo.LocalAddresses) > 0 {
			localAddress := networkInfo.LocalAddresses[0].Address
			localPort := networkInfo.LocalAddresses[0].Port
			localHost := fmt.Sprintf("%s:%d", localAddress, localPort)
			results, err := sc.dashCoreRPCClient.MasternodeListJSON(localHost)
			if err == nil {
				for _, v := range results {
					decodedProTxHash, err = hex.DecodeString(v.ProTxHash)
					if err != nil {
						return nil, fmt.Errorf("error decoding proTxHash: %v", err)
					}
				}
			}
		}
		if len(decodedProTxHash) != crypto.DefaultHashSize {
			return nil, fmt.Errorf(
				"decoding proTxHash %d is incorrect size when signing proposal : %v",
				len(decodedProTxHash),
				err,
			)
		}
	}

	sc.cachedProTxHash = decodedProTxHash

	return decodedProTxHash, nil
}

// SignVote requests a remote signer to sign a vote
func (sc *DashCoreSignerClient) SignVote(
	ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash,
	protoVote *tmproto.Vote, logger log.Logger) error {
	if len(quorumHash) != crypto.DefaultHashSize {
		return fmt.Errorf("quorum hash is not the right length %s", quorumHash.String())
	}

	quorumSigns, err := types.MakeQuorumSigns(chainID, quorumType, quorumHash, protoVote)
	if err != nil {
		return err
	}

	qs, err := sc.quorumSignAndVerify(ctx, quorumType, quorumHash, quorumSigns.Block)
	if err != nil {
		return err
	}

	// No need to check the error as this is only used for logging
	proTxHash, _ := sc.GetProTxHash(ctx)

	logger.Debug("signed vote", "height", protoVote.Height,
		"round", protoVote.Round,
		"voteType", protoVote.Type,
		"quorumType", quorumType,
		"quorumHash", quorumHash,
		"signature", hex.EncodeToString(qs.sign),
		"proTxHash", proTxHash,
		"coreBlockRequestId", qs.ID,
		"coreSignId", hex.EncodeToString(tmbytes.Reverse(qs.signHash)),
		"signItem", quorumSigns,
		"signResult", qs,
	)

	protoVote.BlockSignature = qs.sign

	return sc.signVoteExtensions(ctx, quorumType, quorumHash, protoVote, quorumSigns)
}

// SignProposal requests a remote signer to sign a proposal
func (sc *DashCoreSignerClient) SignProposal(
	ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash, proposalProto *tmproto.Proposal,
) (tmbytes.HexBytes, error) {
	signItem := types.NewSignItem(
		quorumType,
		quorumHash,
		types.ProposalRequestIDProto(proposalProto),
		types.ProposalBlockSignBytes(chainID, proposalProto),
	)
	resp, err := sc.quorumSignAndVerify(ctx, quorumType, quorumHash, signItem)
	if err != nil {
		return nil, err
	}
	proposalProto.Signature = resp.sign
	return nil, nil
}

// QuorumSign implements DashPrivValidator
func (sc *DashCoreSignerClient) QuorumSign(
	ctx context.Context,
	msgHash []byte,
	requestIDHash []byte,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) ([]byte, []byte, error) {
	signItem := types.NewSignItemFromHash(quorumType, quorumHash, requestIDHash, msgHash)

	qs, err := sc.quorumSignAndVerify(ctx, quorumType, quorumHash, signItem)
	if err != nil {
		return nil, nil, err
	}
	return qs.sign, qs.signHash, nil
}

func (sc *DashCoreSignerClient) UpdatePrivateKey(
	_ctx context.Context,
	_privateKey crypto.PrivKey,
	_quorumHash crypto.QuorumHash,
	_thresholdPublicKey crypto.PubKey,
	_height int64,
) {

}

func (sc *DashCoreSignerClient) GetPrivateKey(_ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PrivKey, error) {
	key := &dashConsensusPrivateKey{
		quorumHash: quorumHash,
		quorumType: sc.defaultQuorumType,
		privval:    sc,
	}

	return key, nil
}

// QuorumVerify implements dashcore.QuorumVerifier
func (sc *DashCoreSignerClient) QuorumVerify(
	quorumType btcjson.LLMQType,
	requestID tmbytes.HexBytes,
	messageHash tmbytes.HexBytes,
	signature tmbytes.HexBytes,
	quorumHash tmbytes.HexBytes,
) (bool, error) {
	return sc.dashCoreRPCClient.QuorumVerify(quorumType, requestID, messageHash, signature, quorumHash)
}

// DashRPCClient implements DashPrivValidator
func (sc *DashCoreSignerClient) DashRPCClient() dashcore.Client {
	if sc == nil {
		return nil
	}
	return sc.dashCoreRPCClient
}

func (sc *DashCoreSignerClient) signVoteExtensions(
	ctx context.Context,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	protoVote *tmproto.Vote,
	quorumSignData types.QuorumSignData,
) error {
	sc.logger.Trace("signing vote extensions", "vote", protoVote)

	if protoVote.Type != tmproto.PrecommitType {
		if len(protoVote.VoteExtensions) > 0 {
			return errors.New("unexpected vote extension - extensions are only allowed in precommits")
		}
		return nil
	}

	for i, ext := range quorumSignData.VoteExtensionSignItems {
		signItem := ext
		resp, err := sc.quorumSignAndVerify(ctx, quorumType, quorumHash, signItem)
		if err != nil {
			return err
		}

		protoVote.VoteExtensions[i].Signature = resp.sign
	}

	sc.logger.Trace("vote extensions signed", "extensions", protoVote.VoteExtensions)

	return nil
}

func (sc *DashCoreSignerClient) quorumSignAndVerify(
	ctx context.Context,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	signItem types.SignItem,
) (*quorumSignResult, error) {
	qs, err := sc.quorumSign(quorumType, quorumHash, signItem)
	if err != nil {
		return nil, err
	}
	sc.logger.Trace("quorum sign result",
		"sign", hex.EncodeToString(qs.sign),
		"sign_hash", hex.EncodeToString(qs.signHash),
		"req_id", hex.EncodeToString(signItem.ID),
		"id", hex.EncodeToString(signItem.SignHash),
		"raw", hex.EncodeToString(signItem.Msg),
		"hash", hex.EncodeToString(signItem.MsgHash),
		"quorum_sign_result", *qs.QuorumSignResult)
	pubKey, err := sc.GetPubKey(ctx, quorumHash)
	if err != nil {
		return nil, &RemoteSignerError{Code: 500, Description: err.Error()}
	}
	verified := pubKey.VerifySignatureDigest(signItem.SignHash, qs.sign)
	if !verified {
		return nil, fmt.Errorf("unable to verify signature with pubkey %s", pubKey.String())
	}
	return qs, nil
}

func (sc *DashCoreSignerClient) quorumSign(
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	signItem types.SignItem,
) (*quorumSignResult, error) {
	resp, err := sc.dashCoreRPCClient.QuorumSign(quorumType, signItem.ID, signItem.MsgHash, quorumHash)
	if err != nil {
		return nil, &RemoteSignerError{Code: 500, Description: "cannot sign vote: " + err.Error()}
	}
	if resp == nil {
		return nil, ErrUnexpectedResponse
	}
	sign, err := hex.DecodeString(resp.Signature)
	if err != nil {
		return nil, fmt.Errorf("error decoding signature when signing vote : %v", err)
	}
	if len(sign) != bls12381.SignatureSize {
		return nil, fmt.Errorf("decoding signature %d is incorrect size when signing vote : %v", len(sign), err)
	}
	signHash, err := hex.DecodeString(resp.SignHash)
	if err != nil {
		return nil, fmt.Errorf("error decoding coreSignID when signing vote : %v", err)
	}
	return &quorumSignResult{resp, sign, signHash}, nil
}

type quorumSignResult struct {
	*btcjson.QuorumSignResult
	sign     []byte
	signHash []byte
}
