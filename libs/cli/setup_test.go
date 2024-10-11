package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupEnv(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cases := []struct {
		args     []string
		env      map[string]string
		expected string
	}{
		{nil, nil, ""},
		{[]string{"--foobar", "bang!"}, nil, "bang!"},
		// make sure reset is good
		{nil, nil, ""},
		// test both variants of the prefix
		{nil, map[string]string{"DEMO_FOOBAR": "good"}, "good"},
		{nil, map[string]string{"DEMOFOOBAR": "silly"}, "silly"},
		// and that cli overrides env...
		{[]string{"--foobar", "important"},
			map[string]string{"DEMO_FOOBAR": "ignored"}, "important"},
	}

	for idx, tc := range cases {
		i := strconv.Itoa(idx)
		// test command that store value of foobar in local variable
		var foo string
		demo := &cobra.Command{
			Use: "demo",
			RunE: func(_ *cobra.Command, _ []string) error {
				foo = viper.GetString("foobar")
				return nil
			},
		}
		demo.Flags().String("foobar", "", "Some test value from config")
		cmd := PrepareBaseCmd(demo, "DEMO", "/qwerty/asdfgh") // some missing dir..

		viper.Reset()
		args := append([]string{cmd.Use}, tc.args...)
		err := RunWithArgs(ctx, cmd, args, tc.env)
		require.NoError(t, err, i)
		assert.Equal(t, tc.expected, foo, i)
	}
}

// writeConfigVals writes a toml file with the given values.
// It returns an error if writing was impossible.
func writeConfigVals(dir string, vals map[string]string) error {
	lines := make([]string, 0, len(vals))
	for k, v := range vals {
		lines = append(lines, fmt.Sprintf("%s = %q", k, v))
	}
	data := strings.Join(lines, "\n")
	cfile := filepath.Join(dir, "config.toml")
	return os.WriteFile(cfile, []byte(data), 0600)
}

func TestSetupConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// we pre-create two config files we can refer to in the rest of
	// the test cases.
	cval1 := "fubble"
	conf1 := t.TempDir()
	err := writeConfigVals(conf1, map[string]string{"boo": cval1})
	require.NoError(t, err)

	cases := []struct {
		args        []string
		env         map[string]string
		expected    string
		expectedTwo string
	}{
		{nil, nil, "", ""},
		// setting on the command line
		{[]string{"--boo", "haha"}, nil, "haha", ""},
		{[]string{"--two-words", "rocks"}, nil, "", "rocks"},
		{[]string{"--home", conf1}, nil, cval1, ""},
		// test both variants of the prefix
		{nil, map[string]string{"RD_BOO": "bang"}, "bang", ""},
		{nil, map[string]string{"RD_TWO_WORDS": "fly"}, "", "fly"},
		{nil, map[string]string{"RDTWO_WORDS": "fly"}, "", "fly"},
		{nil, map[string]string{"RD_HOME": conf1}, cval1, ""},
		{nil, map[string]string{"RDHOME": conf1}, cval1, ""},
	}

	for idx, tc := range cases {
		i := strconv.Itoa(idx)
		// test command that store value of foobar in local variable
		var foo, two string
		boo := &cobra.Command{
			Use: "reader",
			RunE: func(_ *cobra.Command, _ []string) error {
				foo = viper.GetString("boo")
				two = viper.GetString("two-words")
				return nil
			},
		}
		boo.Flags().String("boo", "", "Some test value from config")
		boo.Flags().String("two-words", "", "Check out env handling -")
		cmd := PrepareBaseCmd(boo, "RD", "/qwerty/asdfgh") // some missing dir...

		viper.Reset()
		args := append([]string{cmd.Use}, tc.args...)
		err := RunWithArgs(ctx, cmd, args, tc.env)
		require.NoError(t, err, i)
		assert.Equal(t, tc.expected, foo, i)
		assert.Equal(t, tc.expectedTwo, two, i)
	}
}

type DemoConfig struct {
	Name   string `mapstructure:"name"`
	Age    int    `mapstructure:"age"`
	Unused int    `mapstructure:"unused"`
}

func TestSetupUnmarshal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// we pre-create two config files we can refer to in the rest of
	// the test cases.
	cval1, cval2 := "someone", "else"
	conf1 := t.TempDir()
	err := writeConfigVals(conf1, map[string]string{"name": cval1})
	require.NoError(t, err)
	// even with some ignored fields, should be no problem
	conf2 := t.TempDir()
	err = writeConfigVals(conf2, map[string]string{"name": cval2, "foo": "bar"})
	require.NoError(t, err)

	// unused is not declared on a flag and remains from base
	base := DemoConfig{
		Name:   "default",
		Age:    42,
		Unused: -7,
	}
	c := func(name string, age int) DemoConfig {
		r := base
		// anything set on the flags as a default is used over
		// the default config object
		r.Name = "from-flag"
		if name != "" {
			r.Name = name
		}
		if age != 0 {
			r.Age = age
		}
		return r
	}

	cases := []struct {
		args     []string
		env      map[string]string
		expected DemoConfig
	}{
		{nil, nil, c("", 0)},
		// setting on the command line
		{[]string{"--name", "haha"}, nil, c("haha", 0)},
		{[]string{"--home", conf1}, nil, c(cval1, 0)},
		// test both variants of the prefix
		{nil, map[string]string{"MR_AGE": "56"}, c("", 56)},
		{nil, map[string]string{"MR_HOME": conf1}, c(cval1, 0)},
		{[]string{"--age", "17"}, map[string]string{"MRHOME": conf2}, c(cval2, 17)},
	}

	for idx, tc := range cases {
		i := strconv.Itoa(idx)
		// test command that store value of foobar in local variable
		cfg := base
		marsh := &cobra.Command{
			Use: "marsh",
			RunE: func(_ *cobra.Command, _ []string) error {
				return viper.Unmarshal(&cfg)
			},
		}
		marsh.Flags().String("name", "from-flag", "Some test value from config")
		// if we want a flag to use the proper default, then copy it
		// from the default config here
		marsh.Flags().Int("age", base.Age, "Some test value from config")
		cmd := PrepareBaseCmd(marsh, "MR", "/qwerty/asdfgh") // some missing dir...

		viper.Reset()
		args := append([]string{cmd.Use}, tc.args...)
		err := RunWithArgs(ctx, cmd, args, tc.env)
		require.NoError(t, err, i)
		assert.Equal(t, tc.expected, cfg, i)
	}
}

func TestSetupTrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cases := []struct {
		args     []string
		env      map[string]string
		long     bool
		expected string
	}{
		{nil, nil, false, "trace flag = false"},
		{[]string{"--trace"}, nil, true, "trace flag = true"},
		{[]string{"--no-such-flag"}, nil, false, "unknown flag: --no-such-flag"},
		{nil, map[string]string{"DBG_TRACE": "true"}, true, "trace flag = true"},
	}

	for idx, tc := range cases {
		i := strconv.Itoa(idx)
		// test command that store value of foobar in local variable
		trace := &cobra.Command{
			Use: "trace",
			RunE: func(_ *cobra.Command, _ []string) error {
				return fmt.Errorf("trace flag = %t", viper.GetBool(TraceFlag))
			},
		}
		cmd := PrepareBaseCmd(trace, "DBG", "/qwerty/asdfgh") // some missing dir..

		viper.Reset()
		args := append([]string{cmd.Use}, tc.args...)
		stdout, stderr, err := runCaptureWithArgs(ctx, cmd, args, tc.env)
		require.Error(t, err, i)
		require.Equal(t, "", stdout, i)
		require.NotEqual(t, "", stderr, i)
		msg := strings.Split(stderr, "\n")
		desired := fmt.Sprintf("ERROR: %s", tc.expected)
		assert.Equal(t, desired, msg[0], i, msg)
		if tc.long && assert.True(t, len(msg) > 2, i) {
			// the next line starts the stack trace...
			assert.Contains(t, stderr, "TestSetupTrace", i)
			assert.Contains(t, stderr, "setup_test.go", i)
		}
	}
}

// runCaptureWithArgs executes the given command with the specified command
// line args and environmental variables set. It returns string fields
// representing output written to stdout and stderr, additionally any error
// from cmd.Execute() is also returned
func runCaptureWithArgs(ctx context.Context, cmd *cobra.Command, args []string, env map[string]string) (stdout, stderr string, err error) {
	oldout, olderr := os.Stdout, os.Stderr // keep backup of the real stdout
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	defer func() {
		os.Stdout, os.Stderr = oldout, olderr // restoring the real stdout
	}()

	// copy the output in a separate goroutine so printing can't block indefinitely
	copyStd := func(reader *os.File) *(chan string) {
		stdC := make(chan string)
		go func() {
			var buf bytes.Buffer
			// io.Copy will end when we call reader.Close() below
			io.Copy(&buf, reader) //nolint:errcheck //ignore error
			select {
			case <-cmd.Context().Done():
			case stdC <- buf.String():
			}
		}()
		return &stdC
	}
	outC := copyStd(rOut)
	errC := copyStd(rErr)

	// now run the command
	err = RunWithArgs(ctx, cmd, args, env)

	// and grab the stdout to return
	wOut.Close()
	wErr.Close()
	stdout = <-*outC
	stderr = <-*errC
	return stdout, stderr, err
}
