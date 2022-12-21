package exec

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTSWriter(t *testing.T) {
	// length of time added to output
	const TimePrefixLen = 11
	type testCase struct {
		input []byte
	}

	testCases := []testCase{
		{nil},
		{[]byte{}},
		{[]byte{'\n'}},
		{[]byte("hi")},
		{[]byte("hi\n")},
		{[]byte("test\nnew\nlines\n\n\nWonder if it will work")},
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
			// t.Log(fmt.Sprintf("%x", tc.input[len(tc.input)-1]))
			assert.Len(t, out, len(tc.input)+newlines*TimePrefixLen, "new lines: %d", newlines)
		})
	}
}

func TestTSWriterMultiline(t *testing.T) {
	// length of time added to output
	const TimePrefixLen = 11

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

	buf := bytes.Buffer{}

	writer := &tsWriter{out: &buf, start: time.Now()}
	newlines := 1
	length := 0
	for _, item := range tc {
		_, err := writer.Write(item)
		assert.NoError(t, err)
		length += len(item)
		newlines += bytes.Count(item, []byte{'\n'})
	}
	out := buf.Bytes()

	t.Log("\n" + string(out))
	assert.Len(t, out, length+newlines*TimePrefixLen, "new lines: %d", newlines)
}
