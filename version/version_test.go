package version

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// The release process bumps TMVersionDefault; the RPC OpenAPI document must
// advertise the same version.
func TestOpenAPIVersionMatchesTMVersionDefault(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "rpc", "openapi", "openapi.yaml"))
	require.NoError(t, err)

	want := "\n  version: " + strconv.Quote(TMVersionDefault) + "\n"
	require.Contains(t, string(spec), want,
		"rpc/openapi/openapi.yaml info.version must be %q", TMVersionDefault)
}
