package types

import (
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	cryptoenc "github.com/tendermint/tendermint/crypto/encoding"
	"github.com/tendermint/tendermint/p2p"
	crypto2 "github.com/tendermint/tendermint/proto/tendermint/crypto"
)

func UpdateValidator(proTxHash crypto.ProTxHash,
	pubkeyBytes []byte,
	power int64,
	nodeAddress string,
) ValidatorUpdate {
	valUpdate := ValidatorUpdate{
		Power:       power,
		ProTxHash:   proTxHash,
		NodeAddress: nodeAddress,
	}

	var pke crypto.PubKey
	if len(pubkeyBytes) > 0 {
		pke = bls12381.PubKey(pubkeyBytes)
		pkp, err := cryptoenc.PubKeyToProto(pke)
		if err != nil {
			panic(err)
		}
		valUpdate.PubKey = &pkp
	}

	if nodeAddress != "" {
		addr, err := p2p.ParseNodeAddressWithPubkey(nodeAddress, pke)
		if err != nil {
			panic("cannot parse node address: " + err.Error())
		}
		valUpdate.NodeAddress = addr.String()
	}
	return valUpdate
}

func UpdateValidatorSet(validatorUpdates []ValidatorUpdate, thresholdPublicKey crypto2.PublicKey) ValidatorSetUpdate {
	return ValidatorSetUpdate{
		ValidatorUpdates:   validatorUpdates,
		ThresholdPublicKey: thresholdPublicKey,
	}
}

func UpdateThresholdPublicKey(pk []byte) ThresholdPublicKeyUpdate {
	pke := bls12381.PubKey(pk)
	pkp, err := cryptoenc.PubKeyToProto(pke)
	if err != nil {
		panic(err)
	}

	return ThresholdPublicKeyUpdate{
		ThresholdPublicKey: pkp,
	}
}

func UpdateQuorumHash(quorumHash crypto.QuorumHash) QuorumHashUpdate {
	return QuorumHashUpdate{
		QuorumHash: quorumHash,
	}
}
