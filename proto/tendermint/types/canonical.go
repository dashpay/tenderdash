package types

import (
	"github.com/tendermint/tendermint/internal/libs/protoio"
)

func (c CanonicalBlockID) SignBytes() ([]byte, error) {
	marshaled, err := protoio.MarshalDelimited(&c)
	if err != nil {
		return nil, err
	}

	return marshaled, nil
}
