package types

import (
	"bytes"

	abci "github.com/dashpay/tenderdash/abci/types"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// VoteExtensions is a container where the key is vote-extension type and value is a list of VoteExtension
type VoteExtensions map[tmproto.VoteExtensionType][]tmproto.VoteExtension

// NewVoteExtensionsFromABCIExtended returns vote-extensions container for given ExtendVoteExtension
func NewVoteExtensionsFromABCIExtended(exts []*abci.ExtendVoteExtension) VoteExtensions {
	voteExtensions := make(VoteExtensions)
	for _, ext := range exts {
		ve := ext.ToVoteExtension()
		voteExtensions.Add(ext.Type, ve)
	}
	return voteExtensions
}

// Add creates and adds VoteExtension into a container by vote-extension type
func (e VoteExtensions) Add(t tmproto.VoteExtensionType, ext tmproto.VoteExtension) {
	e[t] = append(e[t], ext)
}

// Validate returns error if an added vote-extension is invalid
func (e VoteExtensions) Validate() error {
	for _, et := range VoteExtensionTypes {
		for _, ext := range e[et] {
			err := ext.Validate()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// IsEmpty returns true if a vote-extension container is empty, otherwise false
func (e VoteExtensions) IsEmpty() bool {
	for _, exts := range e {
		if len(exts) > 0 {
			return false
		}
	}
	return true
}

// ToProto transforms the current state of vote-extension container into VoteExtensions's protobuf
func (e VoteExtensions) ToProto() []*tmproto.VoteExtension {
	extensions := make([]*tmproto.VoteExtension, 0, e.totalCount())
	for _, t := range VoteExtensionTypes {
		for _, ext := range e[t] {
			extensions = append(extensions, &tmproto.VoteExtension{
				Type:      t,
				Extension: ext.Extension,
				Signature: ext.Signature,
			})
		}
	}
	return extensions
}

// ToExtendProto transforms the current state of vote-extension container into ExtendVoteExtension's protobuf
func (e VoteExtensions) ToExtendProto() []*abci.ExtendVoteExtension {
	proto := make([]*abci.ExtendVoteExtension, 0, e.totalCount())
	for _, et := range VoteExtensionTypes {
		for _, ext := range e[et] {
			proto = append(proto, &abci.ExtendVoteExtension{
				Type:      et,
				Extension: ext.Extension,
			})
		}
	}
	return proto
}

// Fingerprint returns a fingerprint of all vote-extensions in a state of this container
func (e VoteExtensions) Fingerprint() []byte {
	cnt := 0
	for _, v := range e {
		cnt += len(v)
	}
	l := make([][]byte, 0, cnt)
	for _, et := range VoteExtensionTypes {
		for _, ext := range e[et] {
			l = append(l, ext.Extension)
		}
	}
	return tmbytes.Fingerprint(bytes.Join(l, nil))
}

// IsSameWithProto compares the current state of the vote-extension with the same in VoteExtensions's protobuf
// checks only the value of extensions
func (e VoteExtensions) IsSameWithProto(proto tmproto.VoteExtensions) bool {
	for t, extensions := range e {
		if len(proto[t]) != len(extensions) {
			return false
		}
		for i, ext := range extensions {
			if !bytes.Equal(ext.Extension, proto[t][i].Extension) {
				return false
			}
		}
	}
	return true
}

func (e VoteExtensions) totalCount() int {
	cnt := 0
	for _, exts := range e {
		cnt += len(exts)
	}
	return cnt
}

// VoteExtensionsFromProto creates VoteExtensions container from VoteExtensions's protobuf
func VoteExtensionsFromProto(pve []*tmproto.VoteExtension) VoteExtensions {
	if pve == nil {
		return nil
	}
	voteExtensions := make(VoteExtensions)
	for _, ext := range pve {
		voteExtensions[ext.Type] = append(voteExtensions[ext.Type], ext.Clone())
	}
	return voteExtensions
}

// Copy creates a deep copy of VoteExtensions
func (e VoteExtensions) Copy() VoteExtensions {
	copied := make(VoteExtensions, len(e))
	for extType, extensions := range e {
		copied[extType] = make([]tmproto.VoteExtension, len(extensions))
		for k, v := range extensions {
			copied[extType][k] = v.Clone()
		}
	}

	return copied
}
