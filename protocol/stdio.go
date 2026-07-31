package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// maxLine bounds one framed message. A provider listing every package on a
// machine is large; a provider that has lost its mind is unbounded, and a
// bufio.Scanner with no limit would grow until the box swaps.
const maxLine = 32 << 20

// ServeStdio runs the provider side of the protocol: one JSON request per line
// on in, one JSON response per line on out.
//
// It returns when in reaches EOF, which is what whoctl closing the pipe means.
// Anything the provider wants to say to a human — the commands it runs under
// -v, a warning — goes to its own stderr, which whoctl passes through; stdout
// belongs to the protocol and nothing else may write to it.
func ServeStdio(ctx context.Context, s *Server, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	encoder := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			// A request that cannot be parsed has no id to answer under, but
			// staying silent would hang whoctl until it gave up.
			resp := Response{Error: &Error{Code: string(core.CodeInvalid), Message: fmt.Sprintf("unreadable request: %v", err)}}
			if err := encoder.Encode(resp); err != nil {
				return err
			}
			continue
		}
		if err := encoder.Encode(s.Handle(ctx, req)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// ServeProcess is a provider binary's whole main: it serves the protocol on
// stdin and stdout until whoctl goes away.
func ServeProcess(new ProviderFunc, version string) error {
	s := NewServer(new)
	s.Version = version
	return ServeStdio(context.Background(), s, os.Stdin, os.Stdout)
}

// Subprocess is a Transport that runs a provider binary.
//
// The child's stderr is wired straight to whoctl's, which is the entire logging
// design: `-v` prints each command as the provider runs it because the provider
// writes it to stderr and nobody in between interprets it. A separate channel
// for logs would mean buffering, ordering rules and a protocol message for
// something the operating system already does.
type Subprocess struct {
	path   string
	args   []string
	stderr io.Writer
	// env replaces the child's environment when set. A provider inherits
	// whoctl's environment by default, which is how steam finds STEAM_API_KEY
	// without whoctl having to know that it exists.
	env []string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

// NewSubprocess prepares a transport for a provider binary. Nothing is started
// until the first call, so registering a provider costs nothing and `whoctl
// version` does not spawn anything.
func NewSubprocess(path string, stderr io.Writer, args ...string) *Subprocess {
	return &Subprocess{path: path, args: args, stderr: stderr}
}

// Call implements Transport.
func (s *Subprocess) Call(ctx context.Context, req Request) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.start(); err != nil {
		return Response{}, err
	}

	wire, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := s.stdin.Write(append(wire, '\n')); err != nil {
		return Response{}, s.died(err)
	}

	if !s.stdout.Scan() {
		if err := s.stdout.Err(); err != nil {
			return Response{}, s.died(err)
		}
		return Response{}, s.died(io.ErrUnexpectedEOF)
	}
	var resp Response
	if err := json.Unmarshal(s.stdout.Bytes(), &resp); err != nil {
		return Response{}, core.Invalidf("provider %s wrote something that is not a response: %w", s.path, err)
	}
	return resp, nil
}

func (s *Subprocess) start() error {
	if s.cmd != nil {
		return nil
	}
	cmd := exec.Command(s.path, s.args...)
	cmd.Stderr = s.stderr
	cmd.Env = s.env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return core.Unavailablef("cannot run provider %s: %w", s.path, err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)

	s.cmd, s.stdin, s.stdout = cmd, stdin, scanner
	return nil
}

// died reports a provider that stopped answering. Waiting for it turns "the
// pipe closed" into the exit status, which is the difference between a useless
// message and one naming a provider that crashed.
func (s *Subprocess) died(cause error) error {
	cmd := s.cmd
	s.cmd, s.stdin, s.stdout = nil, nil, nil
	if cmd == nil {
		return cause
	}
	_ = cmd.Wait()
	if state := cmd.ProcessState; state != nil && !state.Success() {
		return core.Internalf("provider %s exited with %s", s.path, state)
	}
	return core.Internalf("provider %s stopped answering: %w", s.path, cause)
}

// Close shuts the provider down by closing its stdin, which is what ServeStdio
// waits for, and then reaps it.
func (s *Subprocess) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil {
		return nil
	}
	cmd := s.cmd
	_ = s.stdin.Close()
	s.cmd, s.stdin, s.stdout = nil, nil, nil

	err := cmd.Wait()
	// A provider told to go away and going away is not a failure, whatever it
	// decided to exit with.
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}
