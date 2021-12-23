package log

import (
	"encoding/base64"
	"io"

	tmstrings "github.com/tendermint/tendermint/libs/strings"
)

const (
	debugReaderMaxLength = 10240 // Don't log more than 10 kB of data
)

type debugReader struct {
	in     io.Reader
	logger Logger
}

// NewDebugReader creates new io.Reader that will log events to Logger when reading
func NewDebugReader(in io.Reader, logger Logger) io.Reader {
	lr := debugReader{
		in:     in,
		logger: logger,
	}

	return &lr
}

func (lr debugReader) Read(p []byte) (int, error) {
	n, err := lr.in.Read(p)
	if err != nil {
		return n, err
	}
	lr.log(p, n)
	return n, err
}

func (lr debugReader) log(p []byte, len int) {
	if len > debugReaderMaxLength {
		len = debugReaderMaxLength
	}
	data := string(p[:len])
	if !tmstrings.IsASCIIText(data) {
		data = "base64:" + base64.RawStdEncoding.WithPadding(base64.StdPadding).EncodeToString(p[:len])
	}
	lr.logger.Debug("data read", "data", data)
}
