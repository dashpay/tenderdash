package log

import (
	"io"

	sync "github.com/sasha-s/go-deadlock"
)

const (
	// LogFormatPlain defines a logging format used for human-readable text-based
	// logging that is not structured. Typically, this format is used for development
	// and testing purposes.
	LogFormatPlain string = "plain"

	// LogFormatText defines a logging format used for human-readable text-based
	// logging that is not structured. Typically, this format is used for development
	// and testing purposes.
	LogFormatText string = "text"

	// LogFormatJSON defines a logging format for structured JSON-based logging
	// that is typically used in production environments, which can be sent to
	// logging facilities that support complex log parsing and querying.
	LogFormatJSON string = "json"

	// Supported loging levels
	LogLevelTrace = "trace"
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

// Logger defines a generic logging interface compatible with Tendermint.
type Logger interface {
	io.Closer

	// Trace events are logged from the primary path of execution (eg. when everything goes well)
	Trace(msg string, keyVals ...interface{})
	// Debug events are used in non-primary path to provide fine-grained information useful for debugging
	Debug(msg string, keyVals ...interface{})
	// Info events provide general, business-level information about what's happening inside the application, to
	// let the user know what the application is doing
	Info(msg string, keyVals ...interface{})
	// Warn events are used when something unexpected happened, but the application can recover/continue
	Warn(msg string, keyVals ...interface{})
	// Error events are used when something unexpected happened and the application cannot recover, or the
	// issue is serious and the user needs to take action
	Error(msg string, keyVals ...interface{})

	With(keyVals ...interface{}) Logger
}

// syncWriter wraps an io.Writer that can be used in a Logger that is safe for
// concurrent use by multiple goroutines.
type syncWriter struct {
	sync.Mutex
	io.Writer
}

func newSyncWriter(w io.Writer) io.Writer {
	return &syncWriter{Writer: w}
}

// Write writes p to the underlying io.Writer. If another write is already in
// progress, the calling goroutine blocks until the syncWriter is available.
func (w *syncWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(p)
}
