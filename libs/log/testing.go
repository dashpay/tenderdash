package log

import (
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// NewTestingLogger converts a testing.T into a logging interface to
// make test failures and verbose provide better feedback associated
// with test failures. This logging instance is safe for use from
// multiple threads, but in general you should create one of these
// loggers ONCE for each *testing.T instance that you interact with.
//
// By default it collects only ERROR messages, or DEBUG messages in
// verbose mode, and relies on the underlying behavior of
// testing.T.Log()
//
// Users should be careful to ensure that no calls to this logger are
// made in goroutines that are running after (which, by the rules of
// testing.TB will panic.)
func NewTestingLogger(t testing.TB) *TestingLogger {
	t.Helper()
	level := LogLevelError
	if testing.Verbose() {
		level = LogLevelDebug
	}
	return NewTestingLoggerWithLevel(t, level)
}

// NewTestingLoggerWithLevel creates a testing logger instance at a
// specific level that wraps the behavior of testing.T.Log().
func NewTestingLoggerWithLevel(t testing.TB, level string) *TestingLogger {
	t.Helper()
	t.Cleanup(func() {
		// we need time to properly flush all logs, otherwise test can fail with race condition
		time.Sleep(10 * time.Millisecond)
	})
	logLevel, err := zerolog.ParseLevel(level)
	if err != nil {
		t.Fatalf("failed to parse log level (%s): %v", level, err)
	}

	logger := &TestingLogger{
		t:          t,
		assertions: []assertion{},
	}
	logger.defaultLogger = defaultLogger{
		Logger: zerolog.New(logger).Level(logLevel),
	}

	t.Cleanup(func() {
		t.Helper()
		if logger != nil && !logger.AssertionsPassed() {
			t.Fail()
		}
	})

	return logger
}

type TestingLogger struct {
	defaultLogger

	t          testing.TB
	mtx        sync.Mutex
	assertions []assertion
}

type assertion struct {
	match  regexp.Regexp
	passed bool
}

func (tw *TestingLogger) AssertionsPassed() bool {
	tw.t.Helper()

	tw.mtx.Lock()
	defer tw.mtx.Unlock()

	passed := true
	for _, assertion := range tw.assertions {
		if !assertion.passed {
			tw.t.Logf("Assertion FAILED: '%s' not found in the logs", assertion.match.String())
			passed = false
		}
	}
	return passed
}

// AssertMatch definies assertions to check for each subsequent
// log item. It must be called before the log is generated.
// Assertion will pass if at least one log matches regexp `re`.
//
// Note that assertions are only executed on logs matching defined log level.
// Use NewTestingLoggerWithLevel(t, zerolog.LevelDebugValue) to control this.
func (tw *TestingLogger) AssertMatch(re *regexp.Regexp) {
	tw.mtx.Lock()
	defer tw.mtx.Unlock()
	tw.assertions = append(tw.assertions, assertion{match: *re})
	tw.Logger = tw.Logger.Level(zerolog.DebugLevel)
}

// Run implements zerolog.Hook.
// Execute all log assertions against a message.
func (tw *TestingLogger) checkAssertions(msg string) {
	tw.mtx.Lock()
	defer tw.mtx.Unlock()

	for i, assertion := range tw.assertions {
		if assertion.match.MatchString(msg) {
			tw.assertions[i].passed = true
		}
	}
}

func (tw *TestingLogger) Write(in []byte) (int, error) {
	s := string(in)
	tw.t.Log(s)
	tw.checkAssertions(s)
	return len(in), nil
}
