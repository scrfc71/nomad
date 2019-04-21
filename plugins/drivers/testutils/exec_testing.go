package testutils

import (
	"context"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/nomad/plugins/drivers"
	dproto "github.com/hashicorp/nomad/plugins/drivers/proto"
	"github.com/stretchr/testify/require"
)

func ExecTaskStreamingConformanceTests(t *testing.T, driver *DriverHarness, taskID string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		// tests assume unix-ism now
		t.Skip("test assume unix tasks")
	}

	TestExecTaskStreamingBasicResponses(t, driver, taskID)
}

var ExecTaskStreamingBasicCases = []struct {
	Name     string
	Command  string
	Tty      bool
	Stdin    string
	Stdout   string
	Stderr   string
	ExitCode int
}{
	{
		Name:     "notty: basic",
		Command:  "echo hello stdout; echo hello stderr >&2; exit 43",
		Tty:      false,
		Stdout:   "hello stdout\n",
		Stderr:   "hello stderr\n",
		ExitCode: 43,
	},
	{
		Name:     "notty: streaming",
		Command:  "for n in 1 2 3; do echo $n; sleep 1; done",
		Tty:      false,
		Stdout:   "1\n2\n3\n",
		ExitCode: 0,
	},
	{
		Name:     "ntty: stty check",
		Command:  "stty size",
		Tty:      false,
		Stderr:   "stty: standard input: Inappropriate ioctl for device\n",
		ExitCode: 1,
	},
	{
		Name:     "notty: stdin passing",
		Command:  "echo hello from command; head -n1",
		Tty:      false,
		Stdin:    "hello from stdin\n",
		Stdout:   "hello from command\nhello from stdin\n",
		ExitCode: 0,
	},
	{
		Name:    "notty: children processes",
		Command: "(( sleep 3; echo from background ) & ); echo from main; exec sleep 1",
		Tty:     false,
		// when not using tty; wait for all processes to exit matching behavior of `docker exec`
		Stdout:   "from main\nfrom background\n",
		ExitCode: 0,
	},

	// TTY cases - difference is new lines add `\r` and child process waiting is different
	{
		Name:     "tty: basic",
		Command:  "echo hello stdout; echo hello stderr >&2; exit 43",
		Tty:      true,
		Stdout:   "hello stdout\r\nhello stderr\r\n",
		ExitCode: 43,
	},
	{
		Name:     "tty: streaming",
		Command:  "for n in 1 2 3; do echo $n; sleep 1; done",
		Tty:      true,
		Stdout:   "1\r\n2\r\n3\r\n",
		ExitCode: 0,
	},
	{
		Name:     "tty: stty check",
		Command:  "sleep 1; stty size",
		Tty:      true,
		Stdout:   "100 100\r\n",
		ExitCode: 0,
	},
	{
		Name:    "tty: stdin passing",
		Command: "head -n1",
		Tty:     true,
		Stdin:   "hello from stdin\n",
		// in tty mode, we emit line twice: once for tty echoing and one for the actual head output
		Stdout:   "hello from stdin\r\nhello from stdin\r\n",
		ExitCode: 0,
	},
	{
		Name:    "tty: children processes",
		Command: "(( sleep 3; echo from background ) & ); echo from main; exec sleep 1",
		Tty:     true,
		// when using tty; wait for lead process only, like `docker exec -it`
		Stdout:   "from main\r\n",
		ExitCode: 0,
	},
}

func TestExecTaskStreamingBasicResponses(t *testing.T, driver *DriverHarness, taskID string) {
	for _, c := range ExecTaskStreamingBasicCases {
		t.Run(c.Name, func(t *testing.T) {

			stream := newTestExecStream(t, c.Stdin)

			ctx, cancelFn := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancelFn()

			command := []string{"/bin/sh", "-c", c.Command}

			isRaw := false
			exitCode := -2
			if raw, ok := driver.impl.(drivers.ExecTaskStreamingRaw); ok {
				isRaw = true
				err := raw.ExecTaskStreamingRaw(ctx, taskID,
					command, c.Tty, stream)
				require.NoError(t, err)
			} else {
				execOpts, errCh := drivers.StreamsToExecOptions(ctx, command, c.Tty, stream)

				r, err := driver.impl.ExecTaskStreaming(ctx, taskID, execOpts)
				require.NoError(t, err)

				select {
				case err := <-errCh:
					require.NoError(t, err)
				default:
					// all good
				}

				exitCode = r.ExitCode
			}

			result := stream.currentResult()
			require.NoError(t, result.err)

			if isRaw {
				require.Equal(t, c.ExitCode, result.exitCode)
			} else {
				require.Equal(t, c.ExitCode, exitCode)
			}

			require.Equal(t, c.Stdout, result.stdout)
			require.Equal(t, c.Stderr, result.stderr)

		})
	}
}

type execResult struct {
	exitCode int
	stdout   string
	stderr   string

	err error
}

func newTestExecStream(t *testing.T, stdin string) *testExecStream {
	return &testExecStream{
		t:      t,
		stdin:  stdin,
		result: &execResult{exitCode: -2},
	}
}

var _ drivers.ExecTaskStream = (*testExecStream)(nil)

type testExecStream struct {
	t *testing.T

	stdin string

	// input
	recvCalled int

	// result so far
	resultLock sync.Mutex
	result     *execResult
}

func (s *testExecStream) currentResult() execResult {
	s.resultLock.Lock()
	defer s.resultLock.Unlock()

	// make a copy
	return *s.result
}

func (s *testExecStream) Recv() (*drivers.ExecTaskStreamingRequestMsg, error) {
	s.recvCalled++

	switch s.recvCalled {
	case 1:
		return &drivers.ExecTaskStreamingRequestMsg{
			TtySize: &dproto.ExecTaskStreamingRequest_TerminalSize{
				Height: 100,
				Width:  100,
			},
		}, nil
	case 2:
		return &drivers.ExecTaskStreamingRequestMsg{
			Stdin: &dproto.ExecTaskStreamingOperation{
				Data: []byte(s.stdin),
			},
		}, nil
	default:
		return nil, io.EOF

	}
}

func (s *testExecStream) Send(m *drivers.ExecTaskStreamingResponseMsg) error {
	s.resultLock.Lock()
	defer s.resultLock.Unlock()

	switch {
	case m.Stdout != nil && m.Stdout.Data != nil:
		s.t.Logf("received stdout: %s", string(m.Stdout.Data))
		s.result.stdout += string(m.Stdout.Data)
	case m.Stderr != nil && m.Stderr.Data != nil:
		s.t.Logf("received stderr: %s", string(m.Stderr.Data))
		s.result.stderr += string(m.Stderr.Data)
	case m.Exited && m.Result != nil:
		s.result.exitCode = int(m.Result.ExitCode)
	}

	return nil
}
