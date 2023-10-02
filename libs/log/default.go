package log

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
)

var _ Logger = (*defaultLogger)(nil)

type defaultLogger struct {
	zerolog.Logger
	closeFuncs []func() error
}

// NewDefaultLogger returns a default logger that can be used within Tendermint
// and that fulfills the Logger interface. The underlying logging provider is a
// zerolog logger that supports typical log levels along with JSON and plain/text
// log formats.
//
// Since zerolog supports typed structured logging and it is difficult to reflect
// that in a generic interface, all logging methods accept a series of key/value
// pair tuples, where the key must be a string.
func NewDefaultLogger(format, level string) (Logger, error) {
	return NewMultiLogger(format, level, "")
}

// NewMultiLogger creates a new logger that writes to os.Stderr and an additional log file if provided.
// It takes in three parameters: format, level, and additionalLogPath.
// The format parameter specifies the format of the log message.
// The level parameter specifies the minimum log level to write.
// The additionalLogPath parameter specifies the path to the additional log file.
// If additionalLogPath is not empty, the logger writes to both os.Stderr and the additional log file.
// The function returns a Logger interface and an error if any.
//
// See NewDefaultLogger for more details.
func NewMultiLogger(format, level, additionalLogPath string) (Logger, error) {
	var (
		writer    io.Writer = os.Stderr
		closeFunc           = func() error { return nil }
		err       error
	)
	if additionalLogPath != "" {
		file, err := os.OpenFile(additionalLogPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to create log writer: %w", err)
		}
		closeFunc = func() error {
			return file.Close()
		}
		writer = io.MultiWriter(writer, file)
	}
	writer, err = NewFormatter(format, writer)
	if err != nil {
		_ = closeFunc()
		return nil, fmt.Errorf("failed to create log formatter: %w", err)
	}
	logger, err := NewLogger(level, writer)
	if err != nil {
		_ = closeFunc()
		return nil, err
	}
	logger.(*defaultLogger).closeFuncs = append(logger.(*defaultLogger).closeFuncs, closeFunc)

	return logger, nil
}

func NewLogger(level string, logWriter io.Writer) (Logger, error) {
	logLevel, err := zerolog.ParseLevel(level)
	if err != nil {
		return nil, fmt.Errorf("failed to parse log level (%s): %w", level, err)
	}

	// make the writer thread-safe
	logWriter = newSyncWriter(logWriter)

	return &defaultLogger{
		Logger: zerolog.New(logWriter).Level(logLevel).With().Timestamp().Logger(),
	}, nil
}

func (l defaultLogger) Info(msg string, keyVals ...interface{}) {
	l.Logger.Info().Fields(getLogFields(keyVals...)).Msg(msg)
}

func (l defaultLogger) Error(msg string, keyVals ...interface{}) {
	l.Logger.Error().Fields(getLogFields(keyVals...)).Msg(msg)
}

func (l defaultLogger) Debug(msg string, keyVals ...interface{}) {
	l.Logger.Debug().Fields(getLogFields(keyVals...)).Msg(msg)
}

func (l defaultLogger) Trace(msg string, keyVals ...interface{}) {
	l.Logger.Trace().Fields(getLogFields(keyVals...)).Msg(msg)
}

func (l defaultLogger) With(keyVals ...interface{}) Logger {
	return &defaultLogger{Logger: l.Logger.With().Fields(getLogFields(keyVals...)).Logger()}
}
func (l *defaultLogger) Close() (err error) {
	if l == nil {
		return nil
	}
	l.Debug("Closing logger")
	for _, f := range l.closeFuncs {
		if e := f(); e != nil {
			err = multierror.Append(err, e)
		}
	}

	l.closeFuncs = nil

	return err
}

// OverrideWithNewLogger replaces an existing logger's internal with
// a new logger, and makes it possible to reconfigure an existing
// logger that has already been propagated to callers.
func OverrideWithNewLogger(logger Logger, format, level, additionalLogFilePath string) error {
	ol, ok := logger.(*defaultLogger)
	if !ok {
		return fmt.Errorf("logger %T cannot be overridden", logger)
	}

	newLogger, err := NewMultiLogger(format, level, additionalLogFilePath)
	if err != nil {
		return err
	}
	nl, ok := newLogger.(*defaultLogger)
	if !ok {
		newLogger.Close()
		return fmt.Errorf("logger %T cannot be overridden by %T", logger, newLogger)
	}

	if err := ol.Close(); err != nil {
		return err
	}

	ol.Logger = nl.Logger
	ol.closeFuncs = nl.closeFuncs

	return nil
}

// NewFormatter creates a new formatter for the given format. If the format is empty or unsupported then returns error.
func NewFormatter(format string, w io.Writer) (io.Writer, error) {
	switch strings.ToLower(format) {
	case LogFormatPlain, LogFormatText:
		return zerolog.ConsoleWriter{
			Out:        w,
			NoColor:    true,
			TimeFormat: time.RFC3339Nano,
			FormatLevel: func(i interface{}) string {
				if ll, ok := i.(string); ok {
					return strings.ToUpper(ll)
				}
				return "????"
			},
		}, nil
	case LogFormatJSON:
		return w, nil
	}
	return nil, fmt.Errorf("unsupported log format: %s", format)
}

func getLogFields(keyVals ...interface{}) map[string]interface{} {
	if len(keyVals)%2 != 0 {
		return nil
	}

	var fieldName string
	fields := make(map[string]interface{}, len(keyVals))
	for i := 0; i < len(keyVals); i += 2 {
		if val, ok := keyVals[i].(string); ok {
			fieldName = val
		} else {
			fieldName = fmt.Sprint(keyVals[i])
		}
		fields[fieldName] = keyVals[i+1]
	}

	return fields
}

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}
