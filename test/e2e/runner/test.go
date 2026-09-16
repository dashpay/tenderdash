package main

import (
	"context"
	"fmt"
	"os"

	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/test/e2e/pkg/exec"
)

// testParallelism caps concurrently running per-node subtests: it bounds RPC
// load on 4-vCPU CI runners and matches their default GOMAXPROCS.
const testParallelism = 4

// Test runs test cases under tests/
func Test(ctx context.Context, testnet *e2e.Testnet) error {
	err := os.Setenv("E2E_MANIFEST", testnet.File)
	if err != nil {
		return err
	}

	return exec.CommandVerbose(ctx, "./build/tests",
		"-test.count=1",
		"-test.v",
		"-test.timeout=10m",
		fmt.Sprintf("-test.parallel=%d", testParallelism),
	)
}
