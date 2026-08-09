package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ensureFiles(t *testing.T, rootDir string, files ...string) {
	for _, f := range files {
		p := rootify(rootDir, f)
		_, err := os.Stat(p)
		assert.NoError(t, err, p)
	}
}

func TestEnsureRoot(t *testing.T) {
	// setup temp dir for test
	tmpDir := t.TempDir()

	// create root dir
	EnsureRoot(tmpDir)

	require.NoError(t, WriteConfigFile(tmpDir, DefaultConfig()))

	// make sure config is set properly
	data, err := os.ReadFile(filepath.Join(tmpDir, defaultConfigFilePath))
	require.NoError(t, err)

	checkConfig(t, string(data))

	ensureFiles(t, tmpDir, "data")
}

func TestEnsureTestRoot(t *testing.T) {
	testName := "ensureTestRoot"

	// create root dir
	cfg, err := ResetTestRoot(t.TempDir(), testName)
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	rootDir := cfg.RootDir

	// make sure config is set properly
	data, err := os.ReadFile(filepath.Join(rootDir, defaultConfigFilePath))
	require.NoError(t, err)

	checkConfig(t, string(data))

	// TODO: make sure the cfg returned and testconfig are the same!
	baseConfig := DefaultBaseConfig()
	pvConfig := DefaultPrivValidatorConfig()
	ensureFiles(t, rootDir, defaultDataDir, baseConfig.Genesis, pvConfig.Key, pvConfig.State)
}

// The generated config.toml is the only statement of the rate limits most
// operators ever read. It quotes both the cost of the most expensive message and
// the smallest rate that can carry it, and a wrong number there is worse than no
// number: it makes valid settings look invalid and misstates the capacity an
// operator plans for. Both come from the constant the node validates against, so
// the file cannot say one thing while the node enforces another.
func TestGeneratedConfigQuotesTheRateLimitTheNodeEnforces(t *testing.T) {
	tmpDir := t.TempDir()
	EnsureRoot(tmpDir)
	require.NoError(t, WriteConfigFile(tmpDir, DefaultConfig()))

	data, err := os.ReadFile(filepath.Join(tmpDir, defaultConfigFilePath))
	require.NoError(t, err)
	rendered := string(data)

	limit := strconv.Itoa(MinVerificationRateLimit)
	require.Contains(t, rendered,
		"a precommit carrying the maximum vote extensions costs "+limit)
	require.Contains(t, rendered,
		"the value must be 0 (disabled) or at least "+limit)

	// The smallest rate the file calls valid has to be one the node accepts.
	cfg := DefaultConsensusConfig()
	cfg.VerificationRateLimit = float64(MinVerificationRateLimit)
	require.NoError(t, cfg.ValidateBasic())
}

func checkConfig(t *testing.T, configFile string) {
	t.Helper()
	// list of words we expect in the config
	var elems = []string{
		"moniker",
		"seeds",
		"address",
		"create-empty-blocks",
		"verification-rate-limit",
		"peer",
		"timeout",
		"broadcast",
		"send",
		"addr",
		"wal",
		"propose",
		"max",
		"genesis",
	}
	for _, e := range elems {
		if !strings.Contains(configFile, e) {
			t.Errorf("config file was expected to contain %s but did not", e)
		}
	}

	// Overrides for consensus parameters that no longer exist must not be
	// offered to operators: the values would be silently ignored.
	for _, e := range []string{
		"unsafe-commit-timeout-override",
		"unsafe-bypass-commit-timeout-override",
	} {
		if strings.Contains(configFile, e) {
			t.Errorf("config file must not offer %s, it overrides a removed consensus parameter", e)
		}
	}
}
