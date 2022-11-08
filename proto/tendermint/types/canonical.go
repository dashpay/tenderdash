package types

import (
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/libs/protoio"
)

func (c CanonicalBlockID) signBytes() ([]byte, error) {
	marshaled, err := protoio.MarshalDelimited(&c)
	if err != nil {
		return nil, err
	}

	return marshaled, nil
}

func (c CanonicalBlockID) Checksum() ([]byte, error) {
	signBytes, err := c.signBytes()
	if err != nil {
		return nil, err
	}

	return crypto.Checksum(signBytes), nil
}
