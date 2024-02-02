package main

import (
	"context"
	"os"

	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/test/e2e/pkg/exec"
)

// Test runs test cases under tests/
func Test(ctx context.Context, testnet *e2e.Testnet) error {
	err := os.Setenv("E2E_MANIFEST", testnet.File)
	if err != nil {
		return err
	}

	return exec.CommandVerbose(ctx, "./build/tests", "-test.count=1", "-test.v", "-test.timeout=10m")
}
