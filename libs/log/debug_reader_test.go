package log

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDebugReader(t *testing.T) {
	tests := [][]byte{
		[]byte("This is a test reader"),
		{0x00, 0x11, 0x22, 0xfa, 0xbb},
		make([]byte, debugReaderMaxLength+1),
	}

	for _, input := range tests {
		t.Run(
			"",
			func(t *testing.T) {
				logger := NewTMLogger(os.Stdout)
				in := bytes.NewReader(input)
				lr := NewDebugReader(in, logger)
				dataBuffer := make([]byte, debugReaderMaxLength*10)

				n, err := lr.Read(dataBuffer)
				assert.NoError(t, err)
				assert.Equal(t, n, len(input))
				assert.Positive(t, n)
				assert.Equal(t, input, dataBuffer[:n])
			})
	}
}
