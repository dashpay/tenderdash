package require

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Error ...
func Error(t *testing.T, want string, err error) {
	if want == "" {
		require.NoError(t, err)
		return
	}
	require.ErrorContains(t, err, want)
}
