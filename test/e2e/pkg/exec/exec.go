package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"time"
)

// Command executes a shell command.
func Command(ctx context.Context, args ...string) error {
	//nolint: gosec
	// G204: Subprocess launched with a potential tainted input or cmd arguments
	cmd := osexec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	switch err := err.(type) {
	case nil:
		return nil
	case *osexec.ExitError:
		return fmt.Errorf("failed to run %q:\n%v", args, string(out))
	default:
		return err
	}
}

// CommandVerbose executes a shell command while displaying its output.
func CommandVerbose(ctx context.Context, args ...string) error {
	//nolint: gosec
	// G204: Subprocess launched with a potential tainted input or cmd arguments
	cmd := osexec.CommandContext(ctx, args[0], args[1:]...)
	now := time.Now()
	cmd.Stdout = &tsWriter{out: os.Stdout, start: now}
	cmd.Stderr = &tsWriter{out: os.Stderr, start: now}
	return cmd.Run()
}

// tsWriter prepends each item in written data with current timestamp.
// It is used mainly to add info about execution time to output of `e2e runner test`
type tsWriter struct {
	out     io.Writer
	start   time.Time
	tsAdded bool // tsAdded is true if timestamp was already added to current line
}

// Write implements io.Writer
func (w *tsWriter) Write(p []byte) (n int, err error) {
	for n = 0; n < len(p); {
		if !w.tsAdded {
			took := time.Since(w.start)
			ts := fmt.Sprintf("%09.5fs ", took.Seconds())
			if _, err := w.out.Write([]byte(ts)); err != nil {
				return n, err
			}
			w.tsAdded = true
		}

		index := bytes.IndexByte(p[n:], '\n')
		if index < 0 {
			// not found
			index = len(p) - 1 - n
		} else {
			// we have \n, let's add timestamp in next loop
			w.tsAdded = false
		}
		w, err := w.out.Write(p[n : n+index+1])
		n += w
		if err != nil {
			return n, err
		}
	}

	return n, nil
}
