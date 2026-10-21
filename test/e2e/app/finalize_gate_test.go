package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinalizeGate(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "cancel"}[cancelWait], func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "gate")
			require.NoError(t, os.WriteFile(gate, nil, 0600))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- waitForFinalizeGate(ctx, gate) }()
			require.Eventually(t, func() bool { _, err := os.Stat(gate + ".entered"); return err == nil }, time.Second, time.Millisecond)
			select {
			case err := <-done:
				t.Fatalf("application was not held: %v", err)
			default:
			}
			if cancelWait {
				cancel()
			} else {
				require.NoError(t, os.Remove(gate))
			}
			select {
			case err := <-done:
				if cancelWait {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(time.Second):
				t.Fatal("application did not resume")
			}
		})
	}
	require.NoError(t, waitForFinalizeGate(context.Background(), ""))
	require.NoError(t, waitForFinalizeGate(context.Background(), filepath.Join(t.TempDir(), "missing")))
}
