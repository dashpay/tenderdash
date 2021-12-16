//nolint: gosec
package bls12381

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	bls "github.com/shotonoff/bls-signatures/go-bindings"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/tmhash"
	tmjson "github.com/tendermint/tendermint/libs/json"
)

//-------------------------------------

var _ crypto.PrivKey = PrivKey{}

const (
	PrivKeyName = "tendermint/PrivKeyBLS12381"
	PubKeyName  = "tendermint/PubKeyBLS12381"
	// PubKeySize is is the size, in bytes, of public keys as used in this package.
	PubKeySize = 48
	// PrivateKeySize is the size, in bytes, of private keys as used in this package.
	PrivateKeySize = 32
	// SignatureSize of an BLS12381 signature.
	SignatureSize = 96
	// SeedSize is the size, in bytes, of private key seeds. These are the
	// private key representations used by RFC 8032.
	SeedSize = 32

	KeyType = "bls12381"
)

func init() {
	tmjson.RegisterType(PubKey{}, PubKeyName)
	tmjson.RegisterType(PrivKey{}, PrivKeyName)
}

// PrivKey implements crypto.PrivKey.
type PrivKey []byte

// Bytes returns the privkey byte format.
func (privKey PrivKey) Bytes() []byte {
	return privKey
}

// Sign produces a signature on the provided message.
// This assumes the privkey is wellformed in the golang format.
// The first 32 bytes should be random,
// corresponding to the normal bls12381 private key.
// The latter 32 bytes should be the compressed public key.
// If these conditions aren't met, Sign will panic or produce an
// incorrect signature.
func (privKey PrivKey) Sign(msg []byte) ([]byte, error) {
	if len(privKey.Bytes()) != PrivateKeySize {
		panic(errInvalidPrivateKeySize(len(privKey.Bytes())))
	}
	// set modOrder flag to true so that too big random bytes will wrap around and be a valid key
	blsPrivateKey, err := bls.PrivateKeyFromBytes(privKey, true)
	if err != nil {
		return nil, err
	}
	sig := bls.NewAugSchemeMPL().Sign(blsPrivateKey, msg)
	serializedSignature := sig.Serialize()
	// fmt.Printf("signature %X created for msg %X with key %X\n", serializedSignature, msg, privKey.PubKey().Bytes())
	return serializedSignature, nil
}

// SignDigest produces a signature on the provided message.
// This assumes the privkey is wellformed in the golang format.
// The first 32 bytes should be random,
// corresponding to the normal bls12381 private key.
// The latter 32 bytes should be the compressed public key.
// If these conditions aren't met, Sign will panic or produce an
// incorrect signature.
func (privKey PrivKey) SignDigest(msg []byte) ([]byte, error) {
	if len(privKey.Bytes()) != PrivateKeySize {
		panic(errInvalidPrivateKeySize(len(privKey.Bytes())))
	}
	// set modOrder flag to true so that too big random bytes will wrap around and be a valid key
	blsPrivateKey, err := bls.PrivateKeyFromBytes(privKey, true)
	if err != nil {
		return nil, err
	}
	var hash bls.Hash
	copy(hash[:], msg)
	sig := bls.ThresholdSign(blsPrivateKey, hash)
	serializedSignature := sig.Serialize()
	// fmt.Printf("signature %X created for msg %X with key %X\n", serializedSignature, msg, privKey.PubKey().Bytes())
	return serializedSignature, nil
}

// PubKey gets the corresponding public key from the private key.
//
// Panics if the private key is not initialized.
func (privKey PrivKey) PubKey() crypto.PubKey {
	if len(privKey.Bytes()) != PrivateKeySize {
		panic(errInvalidPrivateKeySize(len(privKey.Bytes())))
	}

	// set modOrder flag to true so that too big random bytes will wrap around and be a valid key
	blsPrivateKey, err := bls.PrivateKeyFromBytes(privKey, true)
	if err != nil {
		// should probably change method sign to return an error but since
		// that's not available just panic...
		panic("bad key")
	}
	pk, err := blsPrivateKey.G1Element()
	if err != nil {
		panic(fmt.Errorf("couldn't retrieve a public key from bls private key: %w", err))
	}
	return PubKey(pk.Serialize())
}

// Equals - you probably don't need to use this.
// Runs in constant time based on length of the keys.
func (privKey PrivKey) Equals(other crypto.PrivKey) bool {
	if otherBLS, ok := other.(PrivKey); ok {
		return subtle.ConstantTimeCompare(privKey[:], otherBLS[:]) == 1
	}

	return false
}

func (privKey PrivKey) Type() string {
	return KeyType
}

func (privKey PrivKey) TypeValue() crypto.KeyType {
	return crypto.BLS12381
}

// GenPrivKey generates a new bls12381 private key.
// It uses OS randomness in conjunction with the current global random seed
// in tendermint/libs/common to generate the private key.
func GenPrivKey() PrivKey {
	return genPrivKey(crypto.CReader())
}

// genPrivKey generates a new bls12381 private key using the provided reader.
func genPrivKey(rand io.Reader) PrivKey {
	seed := make([]byte, SeedSize)

	_, err := io.ReadFull(rand, seed)
	if err != nil {
		panic(err)
	}
	sk, err := bls.NewAugSchemeMPL().KeyGen(seed)
	if err != nil {
		panic(err)
	}
	return sk.Serialize()
}

// GenPrivKeyFromSecret hashes the secret with SHA2, and uses
// that 32 byte output to create the private key.
// NOTE: secret should be the output of a KDF like bcrypt,
// if it's derived from user input.
func GenPrivKeyFromSecret(secret []byte) PrivKey {
	seed := crypto.Sha256(secret) // Not Ripemd160 because we want 32 bytes.
	sk, err := bls.NewAugSchemeMPL().KeyGen(seed)
	if err != nil {
		panic(err)
	}
	return sk.Serialize()
}

// ReverseBytes returns a reversed sequence bytes of passed slice
func ReverseBytes(bz []byte) []byte {
	l := len(bz)
	s := make([]byte, l)
	for i, j := 0, l-1; i <= j; i, j = i+1, j-1 {
		s[i], s[j] = bz[j], bz[i]
	}
	return s
}

func ReverseProTxHashes(proTxHashes []crypto.ProTxHash) []crypto.ProTxHash {
	reversedProTxHashes := make([]crypto.ProTxHash, len(proTxHashes))
	for i := 0; i < len(proTxHashes); i++ {
		reversedProTxHashes[i] = ReverseBytes(proTxHashes[i])
	}
	return reversedProTxHashes
}

func CreatePrivLLMQDataDefaultThreshold(members int) ([]crypto.PrivKey, []crypto.ProTxHash, crypto.PubKey) {
	return CreatePrivLLMQData(members, members*2/3+1)
}

func CreateProTxHashes(members int) []crypto.ProTxHash {
	proTxHashes := make([]crypto.ProTxHash, members)
	for i := 0; i < members; i++ {
		proTxHashes[i] = crypto.RandProTxHash()
	}
	return proTxHashes
}

func CreatePrivLLMQData(members int, threshold int) ([]crypto.PrivKey, []crypto.ProTxHash, crypto.PubKey) {
	proTxHashes := CreateProTxHashes(members)
	orderedProTxHashes, skShares, thresholdPublicKey := CreatePrivLLMQDataOnProTxHashes(proTxHashes, threshold)
	return skShares, orderedProTxHashes, thresholdPublicKey
}

func CreatePrivLLMQDataOnProTxHashesDefaultThreshold(proTxHashes []crypto.ProTxHash) ([]crypto.ProTxHash,
	[]crypto.PrivKey, crypto.PubKey) {
	return CreatePrivLLMQDataOnProTxHashes(proTxHashes, len(proTxHashes)*2/3+1)
}

func CreatePrivLLMQDataOnProTxHashesDefaultThresholdUsingSeedSource(proTxHashes []crypto.ProTxHash,
	seedSource int64) ([]crypto.ProTxHash, []crypto.PrivKey, crypto.PubKey) {
	return CreatePrivLLMQDataOnProTxHashesUsingSeed(proTxHashes, len(proTxHashes)*2/3+1, seedSource)
}

func CreatePrivLLMQDataOnProTxHashes(
	proTxHashes []crypto.ProTxHash,
	threshold int,
) ([]crypto.ProTxHash, []crypto.PrivKey, crypto.PubKey) {
	return CreatePrivLLMQDataOnProTxHashesUsingSeed(proTxHashes, threshold, 0)
}

func CreatePrivLLMQDataOnProTxHashesUsingSeed(proTxHashes []crypto.ProTxHash, threshold int,
	seedSource int64) ([]crypto.ProTxHash, []crypto.PrivKey, crypto.PubKey) {
	llmqData, _ := NewLLMQData(proTxHashes, WithThreshold(threshold), WithSeed(seedSource))
	return llmqData.ProTxHashes, llmqData.PrivKeyShares, llmqData.ThresholdPubKey
}

func RecoverThresholdPublicKeyFromPublicKeys(publicKeys []crypto.PubKey, blsIds [][]byte) (crypto.PubKey, error) {
	// if there is only 1 key use it
	if len(publicKeys) == 1 {
		return publicKeys[0], nil
	}
	publicKeyShares := make([]*bls.G1Element, len(publicKeys))
	hashes := make([]bls.Hash, len(publicKeys))
	// Create and validate sigShares for each member and populate BLS-IDs from members into ids
	for i, publicKey := range publicKeys {
		publicKeyShare, err := bls.G1ElementFromBytes(publicKey.Bytes())
		if err != nil {
			return nil, fmt.Errorf("error recovering public key share from bytes %X (size %d - proTxHash %X): %w",
				publicKey.Bytes(), len(publicKey.Bytes()), blsIds[i], err)
		}
		publicKeyShares[i] = publicKeyShare
	}

	for i, blsID := range blsIds {
		if len(blsID) != tmhash.Size {
			return nil, fmt.Errorf("blsID incorrect size in public key recovery, expected 32 bytes (got %d)", len(blsID))
		}
		var hash bls.Hash
		copy(hash[:], ReverseBytes(blsID))
		hashes[i] = hash
	}

	thresholdPublicKey, err := bls.ThresholdPublicKeyRecover(publicKeyShares, hashes)
	if err != nil {
		return nil, fmt.Errorf("error recovering threshold public key from shares: %w", err)
	}
	return PubKey(thresholdPublicKey.Serialize()), nil
}

// RecoverThresholdSignatureFromShares BLS Ids are the Pro_tx_hashes from validators
func RecoverThresholdSignatureFromShares(sigSharesData [][]byte, blsIds [][]byte) ([]byte, error) {
	sigShares := make([]*bls.G2Element, len(sigSharesData))
	hashes := make([]bls.Hash, len(sigSharesData))
	if len(sigSharesData) != len(blsIds) {
		return nil, errors.New("the length of the signature shares must match the length of the blsIds")
	}
	// if there is only 1 share use it
	if len(sigSharesData) == 1 {
		return sigSharesData[0], nil
	}
	// Create and validate sigShares for each member and populate BLS-IDs from members into ids
	for i, sigShareData := range sigSharesData {
		sigShare, err := bls.G2ElementFromBytes(sigShareData)
		if err != nil {
			return nil, err
		}
		sigShares[i] = sigShare
	}

	for i, blsID := range blsIds {
		if len(blsID) != tmhash.Size {
			return nil, fmt.Errorf("blsID incorrect size in signature recovery, expected 32 bytes (got %d)", len(blsID))
		}
		var hash bls.Hash
		copy(hash[:], ReverseBytes(blsID))
		hashes[i] = hash
	}

	thresholdSignature, err := bls.ThresholdSignatureRecover(sigShares, hashes)
	if err != nil {
		return nil, err
	}
	return thresholdSignature.Serialize(), nil
}

//-------------------------------------

var _ crypto.PubKey = PubKey{}

// PubKey PubKeyBLS12381 implements crypto.PubKey for the bls12381 signature scheme.
type PubKey []byte

// Address is the SHA256-20 of the raw pubkey bytes.
func (pubKey PubKey) Address() crypto.Address {
	if len(pubKey) != PubKeySize {
		panic("pubkey is incorrect size")
	}
	return crypto.Address(tmhash.SumTruncated(pubKey))
}

// Bytes returns the PubKey byte format.
func (pubKey PubKey) Bytes() []byte {
	return []byte(pubKey)
}

func (pubKey PubKey) VerifySignatureDigest(hash []byte, sig []byte) bool {
	// make sure we use the same algorithm to sign
	if len(sig) == 0 {
		//  fmt.Printf("bls verifying error (signature empty) from message %X with key %X\n", msg, pubKey.Bytes())
		return false
	}
	if len(sig) != SignatureSize {
		// fmt.Printf("bls verifying error (signature size) sig %X from message %X with key %X\n", sig, msg, pubKey.Bytes())
		return false
	}
	publicKey, err := bls.G1ElementFromBytes(pubKey)
	if err != nil {
		// fmt.Printf("bls verifying error (publicKey) sig %X from message %X with key %X\n", sig, msg, pubKey.Bytes())
		return false
	}
	blsSignature, err := bls.G2ElementFromBytes(sig)
	if err != nil {
		// fmt.Printf("bls verifying error (blsSignature) sig %X from message %X with key %X\n", sig, msg, pubKey.Bytes())
		return false
	}
	var h bls.Hash
	copy(h[:], hash)
	verified := bls.ThresholdVerify(publicKey, h, blsSignature)
	//  if !verified {
	//	  fmt.Printf("bls verified (%t) sig %X from message %X with key %X\n", verified, sig, msg, pubKey.Bytes())
	//	  debug.PrintStack()
	//  }
	return verified
}

func (pubKey PubKey) VerifySignature(msg []byte, sig []byte) bool {
	// make sure we use the same algorithm to sign
	if len(sig) == 0 {
		//  fmt.Printf("bls verifying error (signature empty) from message %X with key %X\n", msg, pubKey.Bytes())
		return false
	}
	if len(sig) != SignatureSize {
		// fmt.Printf("bls verifying error (signature size) sig %X from message %X with key %X\n", sig, msg, pubKey.Bytes())
		return false
	}
	publicKey, err := bls.G1ElementFromBytes(pubKey)
	if err != nil {
		// fmt.Printf("bls verifying error (publicKey) sig %X from message %X with key %X\n", sig, msg, pubKey.Bytes())
		return false
	}
	sig1, err := bls.G2ElementFromBytes(sig)
	if err != nil {
		// fmt.Printf("bls verifying error (blsSignature) sig %X from message %X with key %X\n", sig, msg, pubKey.Bytes())
		return false
	}
	verified := bls.NewAugSchemeMPL().Verify(publicKey, msg, sig1)
	//  if !verified {
	//	  fmt.Printf("bls verified (%t) sig %X from message %X with key %X\n", verified, sig, msg, pubKey.Bytes())
	//	  debug.PrintStack()
	//  }
	return verified
}

func (pubKey PubKey) String() string {
	return fmt.Sprintf("PubKeyBLS12381{%X}", []byte(pubKey))
}

// HexString returns hex-string representation of pubkey
func (pubKey PubKey) HexString() string {
	return hex.EncodeToString(pubKey)
}

func (pubKey PubKey) TypeValue() crypto.KeyType {
	return crypto.BLS12381
}

func (pubKey PubKey) Type() string {
	return KeyType
}

func (pubKey PubKey) Equals(other crypto.PubKey) bool {
	if otherBLS, ok := other.(PubKey); ok {
		return bytes.Equal(pubKey[:], otherBLS[:])
	}

	return false
}

func errInvalidPrivateKeySize(size int) error {
	return fmt.Errorf("incorrect private key %d bytes but expected %d bytes", size, PrivateKeySize)
}
