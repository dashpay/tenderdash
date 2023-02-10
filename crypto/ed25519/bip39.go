package ed25519

import (
	"fmt"

	"github.com/oasisprotocol/oasis-core/go/common/crypto/sakg"
	"github.com/oasisprotocol/oasis-core/go/common/crypto/signature"
	"github.com/oasisprotocol/oasis-core/go/common/crypto/slip10"
	"github.com/tyler-smith/go-bip39"
)

// FromBip39Mnemonic derives an ed25519 key from BIP39 mnemonic, following SLIP10 specification.
func FromBip39Mnemonic(mnemonic, passphrase, path string) (PrivKey, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid mnemonic")
	}

	seed := bip39.NewSeed(mnemonic, passphrase)
	signer, chainCode, err := slip10.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("cannot derive master key from mnemonic: %w", err)
	}

	signer, err = deriveFromBip32Path(signer, chainCode, path)
	if err != nil {
		return nil, fmt.Errorf("derive for path %s: %w", path, err)
	}

	unsafeSigner, ok := signer.(signature.UnsafeSigner)
	if !ok {
		return nil, fmt.Errorf("cannot retrieve private key from %T", signer)
	}
	return unsafeSigner.UnsafeBytes(), nil
}

// GetAccountSigner generates a signer for the given mnemonic, passphrase and
// account according to ADR 0008.
func deriveFromBip32Path(signer signature.Signer, chainCode slip10.ChainCode, bip32path string) (signature.Signer, error) {
	path, err := sakg.NewBIP32Path(bip32path)
	if err != nil {
		return nil, fmt.Errorf("invalid path %s: %w", path, err)
	}

	for _, i := range path {
		signer, chainCode, err = slip10.NewChildKey(signer, chainCode, i)
		if err != nil {
			return nil, fmt.Errorf("cannot derive child key %d: %w", i, err)
		}
	}

	return signer, nil
}
