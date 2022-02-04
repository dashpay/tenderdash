package e2e

import (
	"errors"
	"fmt"
	"io"
	"math/rand"

	"github.com/tendermint/tendermint/crypto"
)

// proTxHashGenerator generates pseudorandom proTxHash based on a seed.
type proTxHashGenerator struct {
	random *rand.Rand
}

func newProTxHashGenerator(seed int64) *proTxHashGenerator {
	return &proTxHashGenerator{
		random: rand.New(rand.NewSource(seed)),
	}
}

func (g *proTxHashGenerator) Generate() crypto.ProTxHash {
	seed := make([]byte, crypto.DefaultHashSize)

	_, err := io.ReadFull(g.random, seed)
	if err != nil {
		panic(err) // this shouldn't happen
	}
	return seed
}

// quorumHashGenerator generates pseudorandom quorumHash based on a seed.
type quorumHashGenerator struct {
	random *rand.Rand
}

func newQuorumHashGenerator(seed int64) *quorumHashGenerator {
	return &quorumHashGenerator{
		random: rand.New(rand.NewSource(seed)),
	}
}

func (g *quorumHashGenerator) generate() crypto.QuorumHash {
	seed := make([]byte, crypto.DefaultHashSize)

	_, err := io.ReadFull(g.random, seed)
	if err != nil {
		panic(err) // this shouldn't happen
	}
	return seed
}

type initNodeFunc func(node *Node) error

type validatorParams struct {
	crypto.QuorumKeys
	proTxHash  crypto.ProTxHash
	quorumHash crypto.QuorumHash
}

type validatorParamsIter struct {
	pos                int
	privKeys           []crypto.PrivKey
	proTxHashes        []crypto.ProTxHash
	quorumHash         crypto.QuorumHash
	thresholdPublicKey crypto.PubKey
}

func (i *validatorParamsIter) value() validatorParams {
	privKey := i.privKeys[i.pos]
	return validatorParams{
		QuorumKeys: crypto.QuorumKeys{
			PrivKey:            privKey,
			PubKey:             privKey.PubKey(),
			ThresholdPublicKey: i.thresholdPublicKey,
		},
		proTxHash:  i.proTxHashes[i.pos],
		quorumHash: i.quorumHash,
	}
}

func (i *validatorParamsIter) next() bool {
	if i.pos >= len(i.privKeys) {
		return false
	}
	i.pos++
	return true
}

func withInitNode(hd initNodeFunc, mws ...func(next initNodeFunc) initNodeFunc) initNodeFunc {
	for _, mw := range mws {
		hd = mw(hd)
	}
	return hd
}

func allowedValidator(allowedValidators map[string]int64) func(next initNodeFunc) initNodeFunc {
	return func(next initNodeFunc) initNodeFunc {
		return func(node *Node) error {
			if node.Mode != ModeValidator || len(allowedValidators) == 0 {
				return next(node)
			}
			_, ok := allowedValidators[node.Name]
			if ok {
				return next(node)
			}
			return nil
		}
	}
}

func initValidator(iter *validatorParamsIter, testnet *Testnet) initNodeFunc {
	return func(node *Node) error {
		if node.Mode != ModeValidator {
			return nil
		}
		params := iter.value()
		node.ProTxHash = params.proTxHash
		if node.PrivvalKeys == nil {
			node.PrivvalKeys = make(map[string]crypto.QuorumKeys)
		}
		node.PrivvalKeys[params.quorumHash.String()] = params.QuorumKeys
		testnet.Validators[node] = params.PrivKey.PubKey()
		fmt.Printf("Setting validator %s proTxHash to %s\n", node.Name, node.ProTxHash.ShortString())
		if !iter.next() {
			return errors.New("unable ")
		}
		return nil
	}
}

func initNotValidator(
	thresholdPublicKey crypto.PubKey,
	quorumHash crypto.QuorumHash,
) func(next initNodeFunc) initNodeFunc {
	return func(next initNodeFunc) initNodeFunc {
		return func(node *Node) error {
			if node.Mode == ModeValidator {
				return next(node)
			}
			quorumKeys := crypto.QuorumKeys{
				ThresholdPublicKey: thresholdPublicKey,
			}
			if node.PrivvalKeys == nil {
				node.PrivvalKeys = make(map[string]crypto.QuorumKeys)
			}
			node.PrivvalKeys[quorumHash.String()] = quorumKeys
			return nil
		}
	}
}

func genProTxHashes(proTxHashGen *proTxHashGenerator, n int) []crypto.ProTxHash {
	proTxHashes := make([]crypto.ProTxHash, n)
	for i := 0; i < n; i++ {
		proTxHashes[i] = proTxHashGen.Generate()
		if proTxHashes[i] == nil || len(proTxHashes[i]) != crypto.ProTxHashSize {
			panic("the proTxHash must be 32 bytes")
		}
	}
	return proTxHashes
}

func countValidators(nodes map[string]*ManifestNode) int {
	cnt := 0
	for _, node := range nodes {
		nodeManifest := node
		if nodeManifest.Mode == "" || Mode(nodeManifest.Mode) == ModeValidator {
			cnt++
		}
	}
	return cnt
}

func updateNodeParams(nodes []*Node, initFunc initNodeFunc) error {
	// Set up genesis validators. If not specified explicitly, use all validator nodes.
	for _, node := range nodes {
		err := initFunc(node)
		if err != nil {
			return err
		}
	}
	return nil
}
