package log

import (
	"regexp"
	"testing"
)

func TestTestingMatches(t *testing.T) {
	logger := NewTestingLoggerWithLevel(t, LogLevelDebug)
	logger.AssertMatch(regexp.MustCompile("some text"))
	logger.Debug("some initial text that is no match")
	logger.Debug("this is some text to match")
	logger.Debug("this is another text that should not match")
}
