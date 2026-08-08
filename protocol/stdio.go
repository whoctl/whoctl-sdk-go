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
// A watch runs in its own goroutine so that the requests behind it keep being
// served — asking for something while a table streams is the ordinary case. The
// encoder is serialized because those goroutines share one pipe.
func ServeStdio(ctx context.Context, s *Server, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	encoder := json.NewEncoder(out)

	var write sync.Mutex
	emit := func(resp Response) error {
		write.Lock()
		defer write.Unlock()
		return encoder.Encode(resp)
	}

	// Every stream still running when the pipe closes has to be waited for:
	// returning while one is mid-frame would write to a pipe nobody owns.
	var streams sync.WaitGroup
	defer streams.Wait()

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
			if err := emit(resp); err != nil {
				return err
			}
			continue
		}
		if Streaming(req.Method) {
			streams.Go(func() {
				// A stream that cannot write has lost the pipe, which the read
				// loop is about to notice too. Nothing here can report it.
				_ = s.HandleStream(ctx, req, emit)
			})
			continue
		}
		if err := emit(s.Handle(ctx, req)); err != nil {
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
// # Why it demultiplexes
//
// It used to write a request and read the next line back, which is correct for
// as long as one answer follows one question. A watch breaks that: its frames
// arrive over time, and a `get` issued while one is open would otherwise read
// the watch's next event as its own answer.
//
// So a reader goroutine owns the pipe and routes each response to whoever is
// waiting on its id. Request.ID was always there for this — it was documented
// as being for a transport that pipelines, and this is that transport.
type Subprocess struct {
	path   string
	args   []string
	stderr io.Writer
	// Env replaces the child's environment when set, and must be set before
	// the first call. A provider inherits whoctl's environment by default,
	// which is how steam finds STEAM_API_KEY without whoctl having to know
	// that it exists.
	//
	// It is exported because a whoctl server is the other caller: it runs one
	// provider process per context and builds each one's environment from that
	// context's configuration. That is the whole mechanism by which a server
	// points a provider at one account rather than another, and it is the same
	// mechanism a person uses by exporting a variable in their shell — which is
	// why the provider cannot tell the two apart.
	Env []string

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// pending is who is waiting for which id. A unary call takes one frame off
	// its channel; a stream keeps taking until one arrives with Stream unset.
	pending map[int]*pending
	// closed is shut when the provider stops answering, which wakes everybody
	// waiting rather than leaving them on a channel nothing will ever fill.
	closed  chan struct{}
	readErr error
}

// pending is one caller waiting on the reader goroutine.
type pending struct {
	frames chan Response
	// done is shut when the caller stops listening, so the reader never blocks
	// forever handing a frame to somebody who went away.
	done chan struct{}
}

// NewSubprocess prepares a transport for a provider binary. Nothing is started
// until the first call, so registering a provider costs nothing and `whoctl
// version` does not spawn anything.
func NewSubprocess(path string, stderr io.Writer, args ...string) *Subprocess {
	return &Subprocess{path: path, args: args, stderr: stderr}
}

// Call implements Transport.
func (s *Subprocess) Call(ctx context.Context, req Request) (Response, error) {
	p, closed, err := s.send(req)
	if err != nil {
		return Response{}, err
	}
	defer s.forget(req.ID, p)

	select {
	case resp := <-p.frames:
		return resp, nil
	case <-closed:
		return Response{}, s.death()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}

// CallStream implements Transport.
func (s *Subprocess) CallStream(ctx context.Context, req Request, onFrame func(Response) error) error {
	p, closed, err := s.send(req)
	if err != nil {
		return err
	}
	defer s.forget(req.ID, p)

	for {
		select {
		case resp := <-p.frames:
			if err := onFrame(resp); err != nil {
				return err
			}
			if !resp.Stream {
				return nil
			}
		case <-closed:
			return s.death()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// send starts the provider if it is not running, registers the caller and
// writes the request.
func (s *Subprocess) send(req Request) (*pending, <-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.start(); err != nil {
		return nil, nil, err
	}
	wire, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}

	p := &pending{frames: make(chan Response, 16), done: make(chan struct{})}
	s.pending[req.ID] = p
	closed := s.closed

	if _, err := s.stdin.Write(append(wire, '\n')); err != nil {
		delete(s.pending, req.ID)
		return nil, nil, s.stopped(err)
	}
	return p, closed, nil
}

// forget unregisters a caller and tells the reader to stop trying to reach it.
func (s *Subprocess) forget(id int, p *pending) {
	s.mu.Lock()
	if s.pending[id] == p {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	close(p.done)
}

func (s *Subprocess) start() error {
	if s.cmd != nil {
		return nil
	}
	cmd := exec.Command(s.path, s.args...)
	cmd.Stderr = s.stderr
	cmd.Env = s.Env

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

	s.cmd, s.stdin = cmd, stdin
	s.pending = map[int]*pending{}
	s.closed = make(chan struct{})
	go s.read(stdout, s.closed)
	return nil
}

// read owns the provider's stdout for as long as it is running, routing each
// response to whoever asked for it.
func (s *Subprocess) read(stdout io.Reader, closed chan struct{}) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)

	var failure error
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			failure = core.Invalidf("provider %s wrote something that is not a response: %w", s.path, err)
			break
		}
		s.deliver(resp)
	}
	if failure == nil {
		if failure = scanner.Err(); failure == nil {
			failure = io.ErrUnexpectedEOF
		}
	}

	s.mu.Lock()
	if s.closed == closed {
		s.readErr = failure
		close(closed)
	}
	s.mu.Unlock()
}

// deliver hands a response to its caller, or drops it when nobody is waiting —
// a frame arriving for a watch that has just been forgotten is normal, not an
// error worth failing the whole connection over.
func (s *Subprocess) deliver(resp Response) {
	s.mu.Lock()
	p := s.pending[resp.ID]
	s.mu.Unlock()
	if p == nil {
		return
	}
	select {
	case p.frames <- resp:
	case <-p.done:
	}
}

// death reports a provider that stopped answering. Waiting for it turns "the
// pipe closed" into the exit status, which is the difference between a useless
// message and one naming a provider that crashed.
func (s *Subprocess) death() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cause := s.readErr
	if cause == nil {
		cause = io.ErrUnexpectedEOF
	}
	return s.stopped(cause)
}

// stopped tears the connection down and explains why. It runs under the lock.
func (s *Subprocess) stopped(cause error) error {
	cmd := s.cmd
	s.cmd, s.stdin = nil, nil
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
	if s.cmd == nil {
		s.mu.Unlock()
		return nil
	}
	cmd, stdin := s.cmd, s.stdin
	s.cmd, s.stdin = nil, nil
	s.mu.Unlock()

	_ = stdin.Close()
	err := cmd.Wait()
	// A provider told to go away and going away is not a failure, whatever it
	// decided to exit with.
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}
