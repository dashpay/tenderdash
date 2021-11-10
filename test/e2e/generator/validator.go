package main

import (
	"fmt"
	"math/rand"
	"strconv"
)

const (
	validatorEnabled  = 100
	validatorDisabled = 0
)

type validatorUpdatesPopulator struct {
	rand           *rand.Rand
	initialHeight  int64
	validatorNames []string
	quorumMembers  int
	quorumRotate   int64
}

func (v *validatorUpdatesPopulator) populate(validatorUpdates map[string]map[string]int64) map[string][]int64 {
	nums := len(v.validatorNames)
	updatesLen := (int64(nums/v.quorumMembers) * v.quorumRotate) + v.initialHeight
	if nums%v.quorumMembers > 0 {
		updatesLen++
	}
	var prevHs string
	valHeights := make(map[string][]int64)
	valNames := append([]string(nil), v.validatorNames...)
	for currHeight := v.initialHeight; currHeight < updatesLen; currHeight += v.quorumRotate {
		hs := strconv.FormatInt(currHeight, 10)
		validatorUpdates[hs] = make(map[string]int64)
		if _, ok := validatorUpdates[prevHs]; ok {
			for k, v := range validatorUpdates[prevHs] {
				if v == validatorEnabled {
					validatorUpdates[hs][k] = validatorDisabled
				}
			}
		}
		valNames = v.generateValidators(validatorUpdates[hs], valNames)
		for name, val := range validatorUpdates[hs] {
			if val == validatorEnabled {
				if _, ok := valHeights[name]; ok && valHeights[name][len(valHeights[name])-1] == currHeight {
					continue
				}
				valHeights[name] = append(valHeights[name], currHeight)
			}
		}
		prevHs = hs
	}
	// if initial height greater than 0, then it is necessary to add an update validator set at 0 height
	if v.initialHeight > 0 {
		s := strconv.FormatInt(v.initialHeight, 10)
		validatorUpdates["0"] = validatorUpdates[s]
		for name, val := range valHeights {
			if val[0] == v.initialHeight {
				valHeights[name] = append([]int64{0}, val...)
			}
		}
	}
	return valHeights
}

func (v *validatorUpdatesPopulator) generateValidators(validators map[string]int64, names []string) []string {
	for j := 0; j < v.quorumMembers; j++ {
		for {
			n := v.rand.Intn(len(names))
			name := names[n]
			if _, ok := validators[name]; !ok {
				names = append(names[0:n], names[n+1:]...)
				validators[name] = validatorEnabled
				break
			}
		}
		if len(names) == 0 {
			names = append([]string(nil), v.validatorNames...)
		}
	}
	return names
}

func generateValidatorNames(nums int) []string {
	names := make([]string, 0, nums)
	for i := 1; i <= nums; i++ {
		name := fmt.Sprintf("validator%02d", i)
		names = append(names, name)
	}
	return names
}
