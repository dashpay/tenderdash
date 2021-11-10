package e2e

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
)

func updateOutOfQuorumNodes(
	nodes []*Node,
	height int,
	proTxHashes []crypto.ProTxHash,
	threshold crypto.PubKey,
	quorumHash crypto.QuorumHash,
) {
	set := make(map[string]struct{})
	for _, proTxHash := range proTxHashes {
		set[proTxHash.String()] = struct{}{}
	}
	for _, node := range nodes {
		if _, ok := set[node.ProTxHash.String()]; ok {
			continue
		}
		quorumKeys := crypto.QuorumKeys{
			ThresholdPublicKey: threshold,
		}
		node.PrivvalKeys[quorumHash.String()] = quorumKeys
		node.PrivvalUpdateHeights[strconv.Itoa(height+2)] = quorumHash
	}
}

func prepareProTxHashesFunc(generator *proTxHashGenerator) func(nodes []*Node) []crypto.ProTxHash {
	return func(nodes []*Node) []crypto.ProTxHash {
		proTxHashes := make([]crypto.ProTxHash, 0, len(nodes))
		for _, node := range nodes {
			if node.ProTxHash == nil {
				node.ProTxHash = generator.Generate()
				fmt.Printf("Set validator (at update) %s proTxHash to %X\n", node.Name, node.ProTxHash)
			}
			proTxHashes = append(proTxHashes, node.ProTxHash)
		}
		sort.Sort(crypto.SortProTxHash(proTxHashes))
		return proTxHashes
	}
}

func mapGetStrKeys(m interface{}) []string {
	v := reflect.ValueOf(m)
	if v.Kind() != reflect.Map {
		panic(fmt.Sprintf("passed unsupported type %q, expect %q", v.Kind().String(), reflect.Map.String()))
	}
	mKeys := v.MapKeys()
	keys := make([]string, len(mKeys))
	for i, k := range mKeys {
		keys[i] = k.String()
	}
	return keys
}

func convertStrToIntSlice(in []string) ([]int, error) {
	out := make([]int, len(in))
	for i, s := range in {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

func prepareValidatorUpdateHeights(validatorUpdates map[string]map[string]int64) ([]int, error) {
	heights, err := convertStrToIntSlice(mapGetStrKeys(validatorUpdates))
	if err != nil {
		return nil, err
	}
	sort.Ints(heights)
	return heights, nil
}

func generateValidatorKeys(proTxHashGen *proTxHashGenerator, cnt int) ([]crypto.ProTxHash, []crypto.PrivKey, crypto.PubKey) {
	proTxHashes := make([]crypto.ProTxHash, cnt)
	for i := 0; i < cnt; i++ {
		proTxHashes[i] = proTxHashGen.Generate()
		if proTxHashes[i] == nil || len(proTxHashes[i]) != crypto.ProTxHashSize {
			panic("the proTxHash must be 32 bytes")
		}
	}
	return bls12381.CreatePrivLLMQDataOnProTxHashesDefaultThresholdUsingSeedSource(proTxHashes, randomSeed)
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

func getValidators(manifest Manifest, testnet *Testnet) ([]*Node, error) {
	var validators []*Node
	if manifest.Validators == nil {
		for _, node := range testnet.Nodes {
			if node.Mode == ModeValidator {
				validators = append(validators, node)
			}
		}
		return validators, nil
	}
	for validatorName := range *manifest.Validators {
		validator := testnet.LookupNode(validatorName)
		if validator == nil {
			return nil, fmt.Errorf("unknown validator %q", validatorName)
		}
		validators = append(validators, validator)
	}
	return validators, nil
}
