package exec

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const timePrefixLen = 11

func TestTSWriter(t *testing.T) {
	// length of time added to output
	type testCase struct {
		input     []byte
		expectLen int
	}

	testCases := []testCase{
		{nil, 0},
		{[]byte{}, 0},
		{[]byte{'\n'}, 1},
		{[]byte("hi"), timePrefixLen + 2},
		{[]byte("hi\n"), timePrefixLen + 3},
		{[]byte("test\nnew\nlines\n\n\nWonder if it will work"), timePrefixLen * 6},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			buf := bytes.Buffer{}

			writer := &tsWriter{out: &buf, start: time.Now()}
			_, err := writer.Write(tc.input)
			assert.NoError(t, err)

			out := buf.Bytes()
			newlines := bytes.Count(tc.input, []byte{'\n'})
			if len(tc.input) > 0 && tc.input[len(tc.input)-1] != '\n' {
				//Add initial new line.
				// We don't add it if last char is a new line, as it will only switch the flag to add prefix in next Write()
				newlines++
			}
			assert.Len(t, out, len(tc.input)+newlines*timePrefixLen, "new lines: %d", newlines)
		})
	}
}

func TestTSWriterMultiline(t *testing.T) {
	tc := [][]byte{
		[]byte("Hi\n"),
		[]byte("My name is "),
		[]byte("John Doe."),
		[]byte("\n"),
		[]byte("\n"),
		[]byte("I like drinking coffee.\n"),
		[]byte("\n"),
		{},
		[]byte("\n\n\n"),
		[]byte("This "),
		nil,
		[]byte("is "),
		[]byte("all "),
		[]byte("\nfor today."),
		nil,
		{},
	}
	expectLen := 76 + 10*timePrefixLen

	buf := bytes.Buffer{}

	writer := &tsWriter{out: &buf, start: time.Now()}
	for _, item := range tc {
		_, err := writer.Write(item)
		assert.NoError(t, err)
	}
	out := buf.Bytes()
	assert.Len(t, out, expectLen)
	t.Log("\n" + string(out))
}
