package types

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/cosmos/gogoproto/jsonpb"
	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/crypto"
	cryptoenc "github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/internal/jsontypes"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

const (
	CodeTypeOK uint32 = 0
)

// IsOK returns true if Code is OK.
func (r ResponseCheckTx) IsOK() bool {
	return r.Code == CodeTypeOK
}

// IsErr returns true if Code is something other than OK.
func (r ResponseCheckTx) IsErr() bool {
	return r.Code != CodeTypeOK
}

// IsOK returns true if Code is OK.
func (r ExecTxResult) IsOK() bool {
	return r.Code == CodeTypeOK
}

// IsErr returns true if Code is something other than OK.
func (r ExecTxResult) IsErr() bool {
	return r.Code != CodeTypeOK
}

// IsOK returns true if Code is OK.
func (r ResponseQuery) IsOK() bool {
	return r.Code == CodeTypeOK
}

// IsErr returns true if Code is something other than OK.
func (r ResponseQuery) IsErr() bool {
	return r.Code != CodeTypeOK
}

func (r ResponseProcessProposal) IsAccepted() bool {
	return r.Status == ResponseProcessProposal_ACCEPT
}

func (r ResponseProcessProposal) IsStatusUnknown() bool {
	return r.Status == ResponseProcessProposal_UNKNOWN
}

// IsStatusUnknown returns true if Code is Unknown
func (r ResponseVerifyVoteExtension) IsStatusUnknown() bool {
	return r.Status == ResponseVerifyVoteExtension_UNKNOWN
}

// IsOK returns true if Code is OK
func (r ResponseVerifyVoteExtension) IsOK() bool {
	return r.Status == ResponseVerifyVoteExtension_ACCEPT
}

// IsErr returns true if Code is something other than OK.
func (r ResponseVerifyVoteExtension) IsErr() bool {
	return r.Status != ResponseVerifyVoteExtension_ACCEPT
}

//---------------------------------------------------------------------------
// override JSON marshaling so we emit defaults (ie. disable omitempty)

var (
	jsonpbMarshaller = jsonpb.Marshaler{
		EnumsAsInts:  true,
		EmitDefaults: true,
	}
	jsonpbUnmarshaller = jsonpb.Unmarshaler{}
)

func (r *ResponseCheckTx) MarshalJSON() ([]byte, error) {
	s, err := jsonpbMarshaller.MarshalToString(r)
	return []byte(s), err
}

func (r *ResponseCheckTx) UnmarshalJSON(b []byte) error {
	reader := bytes.NewBuffer(b)
	return jsonpbUnmarshaller.Unmarshal(reader, r)
}

func (r *ResponseQuery) MarshalJSON() ([]byte, error) {
	s, err := jsonpbMarshaller.MarshalToString(r)
	return []byte(s), err
}

func (r *ResponseQuery) UnmarshalJSON(b []byte) error {
	reader := bytes.NewBuffer(b)
	return jsonpbUnmarshaller.Unmarshal(reader, r)
}

func (r *EventAttribute) MarshalJSON() ([]byte, error) {
	s, err := jsonpbMarshaller.MarshalToString(r)
	return []byte(s), err
}

func (r *EventAttribute) UnmarshalJSON(b []byte) error {
	reader := bytes.NewBuffer(b)
	return jsonpbUnmarshaller.Unmarshal(reader, r)
}

// Some compile time assertions to ensure we don't
// have accidental runtime surprises later on.

// jsonEncodingRoundTripper ensures that asserted
// interfaces implement both MarshalJSON and UnmarshalJSON
type jsonRoundTripper interface {
	json.Marshaler
	json.Unmarshaler
}

var _ jsonRoundTripper = (*ResponseQuery)(nil)
var _ jsonRoundTripper = (*ResponseCheckTx)(nil)

var _ jsonRoundTripper = (*EventAttribute)(nil)

type validatorSetUpdateJSON struct {
	ValidatorUpdates []ValidatorUpdate `json:"validator_updates"`
	ThresholdPubKey  json.RawMessage   `json:"threshold_public_key"`
	QuorumHash       []byte            `json:"quorum_hash,omitempty"`
}

func (m *ValidatorSetUpdate) MarshalJSON() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	ret := validatorSetUpdateJSON{
		ValidatorUpdates: m.ValidatorUpdates,
		QuorumHash:       m.QuorumHash,
	}
	if m.ThresholdPublicKey.Sum != nil {
		key, err := cryptoenc.PubKeyFromProto(m.ThresholdPublicKey)
		if err != nil {
			return nil, err
		}
		ret.ThresholdPubKey, err = jsontypes.Marshal(key)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(ret)
}

func (m *ValidatorSetUpdate) UnmarshalJSON(data []byte) error {
	var vsu validatorSetUpdateJSON
	err := json.Unmarshal(data, &vsu)
	if err != nil {
		return err
	}
	if vsu.ThresholdPubKey != nil {
		var key crypto.PubKey
		err = jsontypes.Unmarshal(vsu.ThresholdPubKey, &key)
		if err != nil {
			return err
		}
		m.ThresholdPublicKey, err = cryptoenc.PubKeyToProto(key)
		if err != nil {
			return err
		}
	}
	m.ValidatorUpdates = vsu.ValidatorUpdates
	m.QuorumHash = vsu.QuorumHash
	return nil
}

type validatorUpdateJSON struct {
	PubKey      json.RawMessage `json:"pub_key"`
	Power       int64           `json:"power"`
	ProTxHash   []byte          `json:"pro_tx_hash"`
	NodeAddress string          `json:"node_address"`
}

func (m *ValidatorUpdate) MarshalJSON() ([]byte, error) {
	res := validatorUpdateJSON{
		Power:       m.Power,
		ProTxHash:   m.ProTxHash,
		NodeAddress: m.NodeAddress,
	}
	if m.PubKey != nil {
		pubKey, err := cryptoenc.PubKeyFromProto(*m.PubKey)
		if err != nil {
			return nil, err
		}
		res.PubKey, err = jsontypes.Marshal(pubKey)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(res)
}

func (m *ValidatorUpdate) UnmarshalJSON(b []byte) error {
	var res validatorUpdateJSON
	err := json.Unmarshal(b, &res)
	if err != nil {
		return err
	}
	m.Power = res.Power
	m.NodeAddress = res.NodeAddress
	m.ProTxHash = res.ProTxHash
	if res.PubKey != nil {
		var pubKey crypto.PubKey
		err = jsontypes.Unmarshal(res.PubKey, &pubKey)
		if err != nil {
			return err
		}
		protoPubKey, err := cryptoenc.PubKeyToProto(pubKey)
		if err != nil {
			return err
		}
		m.PubKey = &protoPubKey
	}
	return nil
}

// -----------------------------------------------
// construct Result data

// deterministicExecTxResult constructs a copy of response that omits
// non-deterministic fields. The input response is not modified.
func deterministicExecTxResult(response *ExecTxResult) *ExecTxResult {
	return &ExecTxResult{
		Code:    response.Code,
		Data:    response.Data,
		GasUsed: response.GasUsed,
	}
}

// MarshalTxResults encodes the the TxResults as a list of byte
// slices. It strips off the non-deterministic pieces of the TxResults
// so that the resulting data can be used for hash comparisons and used
// in Merkle proofs.
func MarshalTxResults(r []*ExecTxResult) ([][]byte, error) {
	s := make([][]byte, len(r))
	for i, e := range r {
		d := deterministicExecTxResult(e)
		b, err := d.Marshal()
		if err != nil {
			return nil, err
		}
		s[i] = b
	}
	return s, nil
}

// TxResultsHash determines hash of transaction execution results.
// TODO: light client seems to also include events into LastResultsHash, need to investigate
func TxResultsHash(txResults []*ExecTxResult) (tmbytes.HexBytes, error) {
	rs, err := MarshalTxResults(txResults)
	if err != nil {
		return nil, fmt.Errorf("marshaling TxResults: %w", err)
	}
	return merkle.HashFromByteSlices(rs), nil
}

func (m *ResponsePrepareProposal) Validate() error {
	if !isValidApphash(m.AppHash) {
		return fmt.Errorf("apphash (%X) of size %d is invalid", m.AppHash, len(m.AppHash))
	}
	if m.AppVersion == 0 {
		return fmt.Errorf("app version cannot be 0")
	}

	return nil
}

type Misbehaviors []Misbehavior

func (m Misbehaviors) MarshalZerologArray(e *zerolog.Array) {
	for v := range m {
		e.Interface(v)
	}
}

type Txs [][]byte

func (b Txs) MarshalZerologArray(a *zerolog.Array) {
	for _, bs := range b {
		a.Hex(crypto.Checksum(bs)[:8])
	}
}

func (txr *TxRecord) MarshalZerologObject(e *zerolog.Event) {
	e.Str("action", txr.Action.String())
	e.Hex("tx", crypto.Checksum(txr.Tx)[:8])
}

func (r *RequestPrepareProposal) MarshalZerologObject(e *zerolog.Event) {
	e.Int64("max_tx_bytes", r.MaxTxBytes)
	e.Array("txs", Txs(r.Txs))
	e.Interface("last_commit", r.LocalLastCommit)
	e.Array("misbehavior", Misbehaviors(r.Misbehavior))
	e.Time("proposed_time", r.Time)

	e.Int64("height", r.Height)
	e.Int32("round", r.Round)

	e.Hex("next_validators_hash", r.NextValidatorsHash)
	e.Uint32("core_chain_locked_height", r.CoreChainLockedHeight)
	e.Hex("proposer_pro_tx_hash", r.ProposerProTxHash)
	e.Uint64("proposed_app_version", r.ProposedAppVersion)
	e.Str("version", r.Version.String())
	e.Hex("quorum_hash", r.QuorumHash)
}

func (r *RequestProcessProposal) MarshalZerologObject(e *zerolog.Event) {
	e.Array("txs", Txs(r.Txs))
	e.Interface("last_commit", r.ProposedLastCommit.String())
	e.Array("misbehavior", Misbehaviors(r.Misbehavior))
	e.Time("proposed_time", r.Time)

	e.Hex("block_hash", r.Hash)
	e.Int64("height", r.Height)
	e.Int32("round", r.Round)

	e.Hex("next_validators_hash", r.NextValidatorsHash)
	e.Uint32("core_chain_locked_height", r.CoreChainLockedHeight)
	e.Interface("core_chain_lock_update", r.CoreChainLockUpdate)
	e.Hex("proposer_pro_tx_hash", r.ProposerProTxHash)
	e.Uint64("proposed_app_version", r.ProposedAppVersion)
	e.Str("version", r.Version.String())
	e.Hex("quorum_hash", r.QuorumHash)
}

func isValidApphash(apphash tmbytes.HexBytes) bool {
	return len(apphash) == crypto.DefaultAppHashSize
}

func (r ResponseProcessProposal) Validate() error {
	if !isValidApphash(r.AppHash) {
		return fmt.Errorf("apphash (%X) has wrong size %d, expected: %d", r.AppHash, len(r.AppHash), crypto.DefaultAppHashSize)
	}

	return nil
}

func (m *ValidatorSetUpdate) ProTxHashes() []crypto.ProTxHash {
	ret := make([]crypto.ProTxHash, len(m.ValidatorUpdates))
	for i, v := range m.ValidatorUpdates {
		ret[i] = v.ProTxHash
	}
	return ret
}

func (m *RequestFinalizeBlock) ToCanonicalVote() (types.CanonicalVote, error) {
	cv := types.CanonicalVote{
		Type:    types.PrecommitType,
		Height:  m.Height,
		Round:   int64(m.Round),
		StateID: m.BlockID.StateID,
		ChainID: m.Block.Header.ChainID,
	}
	var err error
	cv.BlockID, err = m.BlockID.ToCanonicalBlockID().Checksum()
	if err != nil {
		return types.CanonicalVote{}, err
	}
	return cv, nil
}

// Convert to proto.types.VoteExtension.
// Signature field will be nil, as ExtendVoteExtension doesn't have it.
func (m *ExtendVoteExtension) ToVoteExtension() types.VoteExtension {
	ve := types.VoteExtension{
		Type:      m.Type,
		Extension: m.Extension,
	}

	// workaround for a bug in gogoproto
	if m.XSignRequestId != nil {
		src := m.GetSignRequestId()

		ve.XSignRequestId = &types.VoteExtension_SignRequestId{
			SignRequestId: bytes.Clone(src),
		}
	}

	return ve
}
