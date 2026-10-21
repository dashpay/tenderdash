package app

import (
	"context"
	"fmt"
	"os"
	"time"
)

// SetFinalizeBlockGate installs a manual-test gate before the application starts.
// Removing the file releases application execution without pausing networking.
func (app *Application) SetFinalizeBlockGate(path string) { app.finalizeGate = path }

func waitForFinalizeGate(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.WriteFile(path+".entered", []byte("FinalizeBlock waiting\n"), 0600); err != nil {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return nil
			} else if err != nil {
				return fmt.Errorf("stat finalize gate: %w", err)
			}
		}
	}
}
