package log

import (
	"regexp"
	"testing"

	"github.com/rs/zerolog"
)

func TestTestingMatches(t *testing.T) {
	logger := NewTestingLoggerWithLevel(t, zerolog.LevelDebugValue)
	logger.AssertMatch(regexp.MustCompile("some text"))
	logger.Debug("some initial text that is no match")
	logger.Debug("this is some text to match")
	logger.Debug("this is another text that should not match")
}
