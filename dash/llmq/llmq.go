package llmq

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
)

var (
	errThresholdInvalid      = errors.New("threshold must be greater than 0")
	errKeySharesNotGenerated = errors.New("to initialize shares you must generate the keys")

	quorumParams = map[btcjson.LLMQType]struct {
		Members   int
		Threshold int
	}{
		btcjson.LLMQType_50_60:            {50, 30},
		btcjson.LLMQType_400_60:           {400, 240},
		btcjson.LLMQType_400_85:           {400, 340},
		btcjson.LLMQType_100_67:           {100, 67},
		btcjson.LLMQType_60_75:            {60, 45},
		btcjson.LLMQType_25_67:            {25, 17},
		btcjson.LLMQType_DEVNET:           {12, 6},
		btcjson.LLMQType_TEST_V17:         {3, 2},
		btcjson.LLMQType_TEST_DIP0024:     {4, 2},
		btcjson.LLMQType_TEST_INSTANTSEND: {3, 2},
		btcjson.LLMQType_DEVNET_DIP0024:   {8, 4},
		btcjson.LLMQType_TEST_PLATFORM:    {3, 2},
		btcjson.LLMQType_DEVNET_PLATFORM:  {12, 8},

		// temporarily commented out due to the default behavior where if llmq type is not found
		// then we take the length of the actual validators as members
		//btcjson.LLMQType_TEST:             {3, 2},
	}
)

type optionFunc func(c *llmqConfig)

// Data contains pre generated keys/shares/signatures for the participants of LLMQ network
type Data struct {
	Threshold        int
	ProTxHashes      []crypto.ProTxHash
	PrivKeys         []crypto.PrivKey
	PubKeys          []crypto.PubKey
	PrivKeyShares    []crypto.PrivKey
	PubKeyShares     []crypto.PubKey
	ThresholdPrivKey crypto.PrivKey
	ThresholdPubKey  crypto.PubKey
}

// blsLLMQData is an intermediate structure that contains the BLS keys/shares/signatures
type blsLLMQData struct {
	proTxHashes []crypto.ProTxHash
	sks         []*bls.PrivateKey
	pks         []*bls.G1Element
	skShares    []*bls.PrivateKey
	pkShares    []*bls.G1Element
}

// QuorumParams returns corresponding LLMQ parameters
// the first integer is an expected size of quorum
// the second is an expected threshold
// the third parameter is an error, it returns in a case if quorumType is not supported
func QuorumParams(quorumType btcjson.LLMQType) (int, int, error) {
	params, ok := quorumParams[quorumType]
	if !ok {
		return 0, 0, fmt.Errorf("quorumType '%d' doesn't match with available", quorumType)
	}
	return params.Members, params.Threshold, nil
}

// MustGenerate generates long-living master node quorum, but panics if a got error
func MustGenerate(proTxHashes []crypto.ProTxHash, opts ...optionFunc) *Data {
	data, err := Generate(proTxHashes, opts...)
	if err != nil {
		panic(err)
	}
	return data
}

// Generate generates long living master node quorum for a list of pro-tx-hashes
// to be able to override default values, need to provide option functions
func Generate(proTxHashes []crypto.ProTxHash, opts ...optionFunc) (*Data, error) {
	conf := llmqConfig{
		proTxHashes: bls12381.ReverseProTxHashes(proTxHashes),
		threshold:   len(proTxHashes)*2/3 + 1,
		seedReader:  cryptorand.Reader,
	}
	for _, opt := range opts {
		opt(&conf)
	}
	err := conf.validate()
	if err != nil {
		return nil, err
	}

	// sorting makes this easier
	sort.Sort(crypto.SortProTxHash(conf.proTxHashes))

	ld, err := initLLMQData(
		conf,
		initKeys(conf.seedReader),
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
			c.seedReader = rand.New(rand.NewSource(seedSource)) //nolint: gosec
		}
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
		return fmt.Errorf("number of proTxHashes %d must be bigger than threshold %d", n, c.threshold)
	}
	for _, proTxHash := range c.proTxHashes {
		if len(proTxHash.Bytes()) != crypto.ProTxHashSize {
			return fmt.Errorf("incorrect proTxHash size in public key recovery, expected 32 bytes (got %d)", len(proTxHash))
		}
	}
	return nil
}

func blsPrivKeys2CPrivKeys(sks []*bls.PrivateKey) []crypto.PrivKey {
	out := make([]crypto.PrivKey, len(sks))
	for i, sk := range sks {
		out[i] = bls12381.PrivKey(sk.Serialize())
	}
	return out
}

func blsPubKeys2CPubKeys(pks []*bls.G1Element) []crypto.PubKey {
	out := make([]crypto.PubKey, len(pks))
	for i, pk := range pks {
		out[i] = bls12381.PubKey(pk.Serialize())
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
		scheme := bls12381.BasicScheme()
		for i := 0; i < len(ld.sks); i++ {
			createdSeed := make([]byte, bls12381.SeedSize)
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

func initShares() func(ld *blsLLMQData) error {
	return func(ld *blsLLMQData) error {
		if len(ld.sks) == 0 || len(ld.pks) == 0 {
			return errKeySharesNotGenerated
		}
		// it is not possible to make private/public and signature shares if a member is only one
		if len(ld.proTxHashes) == 1 {
			ld.skShares = append(ld.skShares, ld.sks...)
			ld.pkShares = append(ld.pkShares, ld.pks...)
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
			pkShare, err := skShare.G1Element()
			if err != nil {
				return err
			}
			ld.pkShares = append(ld.pkShares, pkShare)
		}
		return nil
	}
}

func initValidation() func(ld *blsLLMQData) error {
	return func(ld *blsLLMQData) error {
		l := len(ld.proTxHashes)
		proTxHashes := make([][]byte, l)
		for i := 0; i < l; i++ {
			proTxHashes[i] = ld.proTxHashes[i].ReverseBytes()
		}
		thresholdPubKey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(
			blsPubKeys2CPubKeys(ld.pkShares),
			proTxHashes,
		)
		if err != nil {
			return err
		}
		pk := bls12381.PubKey(ld.pks[0].Serialize())
		if len(pk) == 0 {
			return fmt.Errorf("public key is empty")
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
	}
	for _, init := range inits {
		err := init(&ld)
		if err != nil {
			return ld, err
		}
	}
	return ld, nil
}

func newLLMQDataFromBLSData(ld blsLLMQData, threshold int) *Data {
	llmqData := Data{
		Threshold:     threshold,
		ProTxHashes:   bls12381.ReverseProTxHashes(ld.proTxHashes),
		PrivKeys:      blsPrivKeys2CPrivKeys(ld.sks),
		PrivKeyShares: blsPrivKeys2CPrivKeys(ld.skShares),
		PubKeys:       blsPubKeys2CPubKeys(ld.pks),
		PubKeyShares:  blsPubKeys2CPubKeys(ld.pkShares),
	}
	llmqData.ThresholdPrivKey = llmqData.PrivKeys[0]
	llmqData.ThresholdPubKey = llmqData.PubKeys[0]
	return &llmqData
}

func mustSignShares(proTxHashes []crypto.ProTxHash, signs []*bls.G2Element) ([]byte, [][]byte) {
	if len(proTxHashes) == 0 {
		return nil, nil
	}
	if len(proTxHashes) == 1 {
		blsSigs := blsSigs2CSigs(signs)
		return blsSigs[0], blsSigs
	}
	proTxHashes = bls12381.ReverseProTxHashes(proTxHashes)
	sigShares := make([]*bls.G2Element, len(proTxHashes))
	var id bls.Hash
	for i := 0; i < len(proTxHashes); i++ {
		copy(id[:], proTxHashes[i].Bytes())
		sigShare, err := bls.ThresholdSignatureShare(signs, id)
		sigShares[i] = sigShare
		if err != nil {
			panic(err)
		}
	}
	return signs[0].Serialize(), blsSigs2CSigs(sigShares)
}

func mustThresholdSigns(privKeys []crypto.PrivKey, hash bls.Hash) []*bls.G2Element {
	sigs := make([]*bls.G2Element, len(privKeys))
	for i := 0; i < len(privKeys); i++ {
		sk, err := bls.PrivateKeyFromBytes(privKeys[i].Bytes(), true)
		if err != nil {
			panic(err)
		}
		sigs[i] = bls.ThresholdSign(sk, hash)
	}
	return sigs
}
