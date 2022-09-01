package types

import (
	"bytes"
	"encoding/json"
	fmt "fmt"

	"github.com/gogo/protobuf/jsonpb"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/merkle"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
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
func (r ResponseDeliverTx) IsOK() bool {
	return r.Code == CodeTypeOK
}

// IsErr returns true if Code is something other than OK.
func (r ResponseDeliverTx) IsErr() bool {
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

func (r *ResponseDeliverTx) MarshalJSON() ([]byte, error) {
	s, err := jsonpbMarshaller.MarshalToString(r)
	return []byte(s), err
}

func (r *ResponseDeliverTx) UnmarshalJSON(b []byte) error {
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

func (r *ResponseCommit) MarshalJSON() ([]byte, error) {
	s, err := jsonpbMarshaller.MarshalToString(r)
	return []byte(s), err
}

func (r *ResponseCommit) UnmarshalJSON(b []byte) error {
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

var _ jsonRoundTripper = (*ResponseCommit)(nil)
var _ jsonRoundTripper = (*ResponseQuery)(nil)
var _ jsonRoundTripper = (*ResponseDeliverTx)(nil)
var _ jsonRoundTripper = (*ResponseCheckTx)(nil)

var _ jsonRoundTripper = (*EventAttribute)(nil)

// -----------------------------------------------
// construct Result data

func RespondVerifyVoteExtension(ok bool) ResponseVerifyVoteExtension {
	status := ResponseVerifyVoteExtension_REJECT
	if ok {
		status = ResponseVerifyVoteExtension_ACCEPT
	}
	return ResponseVerifyVoteExtension{
		Status: status,
	}
}

// deterministicExecTxResult constructs a copy of response that omits
// non-deterministic fields. The input response is not modified.
func deterministicExecTxResult(response *ExecTxResult) *ExecTxResult {
	return &ExecTxResult{
		Code:      response.Code,
		Data:      response.Data,
		GasWanted: response.GasWanted,
		GasUsed:   response.GasUsed,
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

func (r ResponsePrepareProposal) Validate() error {
	if !isValidApphash(r.AppHash) {
		return fmt.Errorf("apphash (%X) of size %d is invalid", r.AppHash, len(r.AppHash))
	}
	if len(r.TxRecords) != len(r.TxResults) {
		return fmt.Errorf("tx records len %d does not match tx results len %d", len(r.TxRecords), len(r.TxResults))
	}

	return nil
}

func (r ResponsePrepareProposal) ToResponseProcessProposal() ResponseProcessProposal {
	return ResponseProcessProposal{
		Status:                ResponseProcessProposal_ACCEPT,
		AppHash:               r.AppHash,
		TxResults:             r.TxResults,
		ConsensusParamUpdates: r.ConsensusParamUpdates,
		CoreChainLockUpdate:   r.CoreChainLockUpdate,
		ValidatorSetUpdate:    r.ValidatorSetUpdate,
	}
}

func isValidApphash(apphash tmbytes.HexBytes) bool {
	return len(apphash) >= crypto.SmallAppHashSize && len(apphash) <= crypto.LargeAppHashSize
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
