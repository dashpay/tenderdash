package bls12381

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

var (
	errThresholdInvalid = errors.New("threshold must be greater than 0")
)

type optionFunc func(c *llmqConfig)

// LLMQData contains pre generated keys/shares/signatures for the participants of LLMQ network
type LLMQData struct {
	Threshold        int
	ProTxHashes      []crypto.ProTxHash
	PrivKeys         []crypto.PrivKey
	PubKeys          []crypto.PubKey
	PrivKeyShares    []crypto.PrivKey
	PubKeyShares     []crypto.PubKey
	Sigs             [][]byte
	SigShares        [][]byte
	ThresholdPrivKey crypto.PrivKey
	ThresholdPubKey  crypto.PubKey
	ThresholdSig     []byte
}

// blsLLMQData is an intermediate structure that contains the BLS keys/shares/signatures
type blsLLMQData struct {
	proTxHashes []crypto.ProTxHash
	sks         []*bls.PrivateKey
	pks         []*bls.G1Element
	skShares    []*bls.PrivateKey
	pkShares    []*bls.G1Element
	sigs        []*bls.G2Element
	sigShares   []*bls.G2Element
}

// NewLLMQData generates ling living master node quorum for a list of pro-tx-hashes
// to be able to override default values, need to provide option functions
func NewLLMQData(proTxHashes []crypto.ProTxHash, opts ...optionFunc) (*LLMQData, error) {
	conf := llmqConfig{
		proTxHashes: ReverseProTxHashes(proTxHashes),
		threshold:   len(proTxHashes)*2/3 + 1,
		seedReader:  crypto.CReader(),
	}
	_, err := crypto.CReader().Read(conf.hash[:])
	if err != nil {
		return nil, err
	}
	for _, opt := range opts {
		opt(&conf)
	}
	err = conf.validate()
	if err != nil {
		return nil, err
	}

	// sorting makes this easier
	sort.Sort(crypto.SortProTxHash(conf.proTxHashes))

	ld, err := initLLMQData(
		conf,
		initKeys(conf.seedReader),
		initSigs(conf.hash),
		initShares(),
		// as this is not used in production, we can add this test
		initValidation(),
	)
	if err != nil {
		return nil, err
	}
	return newLLMQDataFromBLSData(ld, conf.threshold), nil
}

// WithSeed sets a seed generator with passed seed-source
func WithSeed(seedSource int64) func(c *llmqConfig) {
	return func(c *llmqConfig) {
		if seedSource > 0 {
			c.seedReader = rand.New(rand.NewSource(seedSource))
		}
	}
}

// WithSignHash sets a signature hash that is used for a signature(s)
func WithSignHash(hash bls.Hash) func(c *llmqConfig) {
	return func(c *llmqConfig) {
		c.hash = hash
	}
}

// WithThreshold sets a threshold number of allowed members for
// a recovery a threshold public key / signature or private key
func WithThreshold(threshold int) func(c *llmqConfig) {
	return func(c *llmqConfig) {
		c.threshold = threshold
	}
}

type llmqConfig struct {
	hash        bls.Hash
	proTxHashes []crypto.ProTxHash
	threshold   int
	seedReader  io.Reader
}

func (c *llmqConfig) validate() error {
	n := len(c.proTxHashes)
	if c.threshold <= 0 {
		return errThresholdInvalid
	}
	if n < c.threshold {
		return fmt.Errorf("n %d must be bigger than threshold %d", n, c.threshold)
	}
	for _, proTxHash := range c.proTxHashes {
		if len(proTxHash.Bytes()) != crypto.ProTxHashSize {
			return fmt.Errorf("blsId incorrect size in public key recovery, expected 32 bytes (got %d)", len(proTxHash))
		}
	}
	return nil
}

func blsPrivKeys2CPrivKeys(sks []*bls.PrivateKey) []crypto.PrivKey {
	out := make([]crypto.PrivKey, len(sks))
	for i, sk := range sks {
		out[i] = PrivKey(sk.Serialize())
	}
	return out
}

func blsPubKeys2CPubKeys(pks []*bls.G1Element) []crypto.PubKey {
	out := make([]crypto.PubKey, len(pks))
	for i, pk := range pks {
		out[i] = PubKey(pk.Serialize())
	}
	return out
}

func blsSigs2CSigs(sigs []*bls.G2Element) [][]byte {
	out := make([][]byte, len(sigs))
	for i, sig := range sigs {
		out[i] = sig.Serialize()
	}
	return out
}

func initKeys(seed io.Reader) func(ld *blsLLMQData) error {
	return func(ld *blsLLMQData) error {
		scheme := bls.NewAugSchemeMPL()
		for i := 0; i < len(ld.sks); i++ {
			createdSeed := make([]byte, SeedSize)
			_, err := io.ReadFull(seed, createdSeed)
			if err != nil {
				return err
			}
			ld.sks[i], err = scheme.KeyGen(createdSeed)
			if err != nil {
				return err
			}
			ld.pks[i], err = ld.sks[i].G1Element()
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func initSigs(hash bls.Hash) func(ld *blsLLMQData) error {
	return func(ld *blsLLMQData) error {
		for i := 0; i < len(ld.sks); i++ {
			ld.sigs = append(ld.sigs, bls.ThresholdSign(ld.sks[i], hash))
		}
		return nil
	}
}

func initShares() func(ld *blsLLMQData) error {
	return func(ld *blsLLMQData) error {
		if len(ld.sks) == 0 || len(ld.pks) == 0 {
			return errors.New("to initialize shares you must generate the keys")
		}
		// it is not possible to make private/public and signature shares if a member is only one
		if len(ld.proTxHashes) == 1 {
			ld.skShares = append(ld.skShares, ld.sks...)
			ld.pkShares = append(ld.pkShares, ld.pks...)
			ld.sigShares = append(ld.sigShares, ld.sigs...)
			return nil
		}
		var id bls.Hash
		for i := 0; i < len(ld.proTxHashes); i++ {
			copy(id[:], ld.proTxHashes[i].Bytes())
			skShare, err := bls.ThresholdPrivateKeyShare(ld.sks, id)
			ld.skShares = append(ld.skShares, skShare)
			if err != nil {
				return err
			}
			pkShare, err := bls.ThresholdPublicKeyShare(ld.pks, id)
			ld.pkShares = append(ld.pkShares, pkShare)
			if err != nil {
				return err
			}
			sigShare, err := bls.ThresholdSignatureShare(ld.sigs, id)
			ld.sigShares = append(ld.sigShares, sigShare)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func initValidation() func(ld *blsLLMQData) error {
	return func(ld *blsLLMQData) error {
		l := len(ld.proTxHashes)
		proTxHashes := make([][]byte, l)
		for i := 0; i < l; i++ {
			proTxHashes[i] = tmbytes.Reverse(ld.proTxHashes[i].Bytes())
		}
		thresholdPubKey, err := RecoverThresholdPublicKeyFromPublicKeys(
			blsPubKeys2CPubKeys(ld.pkShares),
			proTxHashes,
		)
		if err != nil {
			return err
		}
		pk := PubKey(ld.pks[0].Serialize())
		if err != nil {
			return err
		}
		if !thresholdPubKey.Equals(pk) {
			return fmt.Errorf(
				"threshold public keys are not equal, expected \"%X\", given \"%X\"",
				pk.Bytes(),
				thresholdPubKey.Bytes(),
			)
		}
		return nil
	}
}

func initLLMQData(conf llmqConfig, inits ...func(ld *blsLLMQData) error) (blsLLMQData, error) {
	n := len(conf.proTxHashes)
	ld := blsLLMQData{
		proTxHashes: conf.proTxHashes,
		sks:         make([]*bls.PrivateKey, conf.threshold),
		skShares:    make([]*bls.PrivateKey, 0, n),
		pks:         make([]*bls.G1Element, conf.threshold),
		pkShares:    make([]*bls.G1Element, 0, n),
		sigs:        make([]*bls.G2Element, 0, conf.threshold),
		sigShares:   make([]*bls.G2Element, 0, n),
	}
	for _, init := range inits {
		err := init(&ld)
		if err != nil {
			return ld, err
		}
	}
	return ld, nil
}

func newLLMQDataFromBLSData(ld blsLLMQData, threshold int) *LLMQData {
	llmqData := LLMQData{
		Threshold:     threshold,
		ProTxHashes:   ReverseProTxHashes(ld.proTxHashes),
		PrivKeys:      blsPrivKeys2CPrivKeys(ld.sks),
		PrivKeyShares: blsPrivKeys2CPrivKeys(ld.skShares),
		PubKeys:       blsPubKeys2CPubKeys(ld.pks),
		PubKeyShares:  blsPubKeys2CPubKeys(ld.pkShares),
		Sigs:          blsSigs2CSigs(ld.sigs),
		SigShares:     blsSigs2CSigs(ld.sigShares),
	}
	llmqData.ThresholdPrivKey = llmqData.PrivKeys[0]
	llmqData.ThresholdPubKey = llmqData.PubKeys[0]
	if len(llmqData.Sigs) > 0 {
		llmqData.ThresholdSig = llmqData.Sigs[0]
	}
	return &llmqData
}
