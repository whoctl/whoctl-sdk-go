package protocol

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// This test binary doubles as a provider binary. Re-executing ourselves is what
// makes a real subprocess testable without building anything: the transport
// spawns a process, writes framed JSON to its stdin and reads its stdout, which
// is exactly what it will do to whoctl-provider-linux.
const provideEnv = "WHOCTL_TEST_BE_A_PROVIDER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(provideEnv); mode != "" {
		os.Exit(beAProvider(mode))
	}
	os.Exit(m.Run())
}

func beAProvider(mode string) int {
	switch mode {
	case "crash":
		fmt.Fprintln(os.Stderr, "provider: falling over")
		return 3
	case "garbage":
		fmt.Println("this is not a response")
		return 0
	}

	err := ServeProcess(func(cfg Config) (core.Provider, error) {
		// Prove the configuration crossed: whatever whoctl passed as --root
		// comes back as a widget's colour, which nothing else could produce.
		h := &widgetHandler{}
		if cfg.Verbose {
			// This is what `-v` looks like from a provider: a line on stderr,
			// which whoctl passes through without interpreting.
			fmt.Fprintf(os.Stderr, "provider: running with root=%s dryRun=%t\n", cfg.Root, cfg.DryRun)
		}
		return &fakeProvider{widget: h}, nil
	}, "test")
	if err != nil {
		fmt.Fprintln(os.Stderr, "provider:", err)
		return 1
	}
	return 0
}

// syncBuffer is the provider's stderr. It has to be locked: the child's stderr
// is copied into it by a goroutine os/exec owns, and a test reading it is a
// second one. Without this the suite cannot be run under -race at all, which is
// how a transport with goroutines of its own stops being checkable.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func subprocessClient(t *testing.T, cfg Config, mode string) (*Client, *syncBuffer, *Subprocess) {
	t.Helper()
	stderr := &syncBuffer{}
	transport := NewSubprocess(os.Args[0], stderr)
	transport.Env = append(os.Environ(), provideEnv+"="+mode)

	client, err := Connect(context.Background(), transport, cfg)
	if err != nil {
		t.Fatalf("connect: %v\nprovider stderr:\n%s", err, stderr.String())
	}
	t.Cleanup(func() { _ = transport.Close() })
	return client, stderr, transport
}

// The whole arrangement in one test: a provider in another process, answering.
func TestASubprocessProviderAnswers(t *testing.T) {
	client, _, _ := subprocessClient(t, Config{Root: "/fixture"}, "serve")

	if client.Name() != "test" {
		t.Errorf("name = %q", client.Name())
	}
	h := handlerFor(t, client, "Widget")
	objs, err := h.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].Metadata.Name != "one" {
		t.Fatalf("objects = %v", objs)
	}
	// Field order has to survive a real pipe too, not just a loopback.
	if spec, ok := objs[0].Spec.(*Map); !ok || len(spec.Keys()) == 0 || spec.Keys()[0] != "size" {
		t.Errorf("spec keys = %v, want size first", spec.Keys())
	}
	if got := core.Lookup(objs[0], "status.observed"); got != int64(1700000000) {
		t.Errorf("observed = %#v, want an integer across the pipe", got)
	}
}

// More than one call has to work on one process: the transport keeps it alive
// and frames each request, rather than spawning a provider per command.
func TestOneProcessServesManyCalls(t *testing.T) {
	client, _, _ := subprocessClient(t, Config{}, "serve")
	h := handlerFor(t, client, "Widget")
	for i := range 5 {
		if _, err := h.Get(context.Background(), "one"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

// `-v` prints each command as the provider runs it. There is no protocol
// message for that: the provider writes to its stderr and whoctl passes it
// through, which is the entire design.
func TestTheProvidersStderrReachesTheUser(t *testing.T) {
	client, stderr, _ := subprocessClient(t, Config{Root: "/fixture", Verbose: true, DryRun: true}, "serve")
	if _, err := handlerFor(t, client, "Widget").List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stderr.String(); !strings.Contains(got, "root=/fixture dryRun=true") {
		t.Errorf("stderr = %q, want the provider's own log line", got)
	}
}

// Error codes have to survive a pipe, or `delete --ignore-not-found` stops
// working the moment a provider moves out of process.
func TestErrorCodesSurviveTheSubprocess(t *testing.T) {
	client, _, _ := subprocessClient(t, Config{}, "serve")
	h := handlerFor(t, client, "Widget")

	if _, err := h.Get(context.Background(), "nope"); !core.IsNotFound(err) {
		t.Errorf("code = %q, want NOT_FOUND: %v", core.CodeOf(err), err)
	}
	if err := h.Delete(context.Background(), "one"); core.CodeOf(err) != core.CodeRefused {
		t.Errorf("code = %q, want REFUSED: %v", core.CodeOf(err), err)
	}
}

// A provider that dies must say so in terms of the provider, not in terms of a
// pipe: "EOF" tells nobody which of their providers fell over.
func TestAProviderThatDiesIsReportedAsOne(t *testing.T) {
	var stderr bytes.Buffer
	transport := NewSubprocess(os.Args[0], &stderr)
	transport.Env = append(os.Environ(), provideEnv+"=crash")

	_, err := Connect(context.Background(), transport, Config{})
	if err == nil {
		t.Fatal("connecting to a provider that exits must fail")
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("err = %v, want it to name the exit", err)
	}
	if !strings.Contains(stderr.String(), "falling over") {
		t.Errorf("the provider's last words were lost: %q", stderr.String())
	}
}

// Anything on stdout that is not a response is a provider bug, and the message
// has to say so — stdout belongs to the protocol.
func TestAProviderWritingToStdoutIsReported(t *testing.T) {
	var stderr bytes.Buffer
	transport := NewSubprocess(os.Args[0], &stderr)
	transport.Env = append(os.Environ(), provideEnv+"=garbage")

	_, err := Connect(context.Background(), transport, Config{})
	if err == nil {
		t.Fatal("a provider writing prose to stdout must fail")
	}
	if !strings.Contains(err.Error(), "not a response") {
		t.Errorf("err = %v", err)
	}
}

// Closing is what tells a provider to go away: ServeStdio returns at EOF.
func TestCloseStopsTheProvider(t *testing.T) {
	client, _, transport := subprocessClient(t, Config{}, "serve")
	if _, err := handlerFor(t, client, "Widget").List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	// Closing twice is what a deferred shutdown after an explicit one does.
	if err := transport.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

// The framing is one message per line, so nothing a provider returns may
// contain a raw newline.
func TestFramingSurvivesNewlinesInTheData(t *testing.T) {
	in, inWriter := io.Pipe()
	outReader, out := io.Pipe()

	go func() {
		_ = ServeStdio(context.Background(), NewServerOf(&fakeProvider{widget: &widgetHandler{}}), in, out)
		_ = out.Close()
	}()

	go func() {
		fmt.Fprintf(inWriter, "%s\n", `{"id":1,"method":"handshake","params":{"protocol":"`+Version+`"}}`)
	}()

	buf := make([]byte, 4096)
	n, err := outReader.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	line := buf[:n]
	if bytes.Count(bytes.TrimRight(line, "\n"), []byte("\n")) != 0 {
		t.Errorf("a response spans more than one line: %q", line)
	}
	_ = inWriter.Close()
}

// The whole reason the transport demultiplexes, across a real process
// boundary: a watch is open and its frames are arriving, and an ordinary get
// goes down the same pipe and comes back with its own answer.
//
// The old transport wrote a request and read the next line back. Here that line
// is the watch's next event, so the get would have returned somebody else's
// answer — and it would have looked like a decoding bug, not a framing one.
func TestAWatchAndAGetShareOnePipe(t *testing.T) {
	client, stderr, _ := subprocessClient(t, Config{}, "serve")
	widget := handlerFor(t, client, "Widget")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan string, 8)
	watched := make(chan error, 1)
	go func() {
		watched <- widget.(core.Watcher).Watch(ctx, func(e core.Event) error {
			events <- e.Object.Metadata.Name
			return nil
		})
	}()

	// Wait until the stream is really running, so the get below is issued with
	// a watch open rather than racing it.
	if first := <-events; first != "one" {
		t.Fatalf("first event = %q, want %q", first, "one")
	}

	obj, err := widget.Get(ctx, "one")
	if err != nil {
		t.Fatalf("get while watching: %v\nprovider stderr:\n%s", err, stderr.String())
	}
	if obj.Metadata.Name != "one" || obj.Kind != "Widget" {
		t.Errorf("the get was answered with %s %q", obj.Kind, obj.Metadata.Name)
	}
	if second := <-events; second != "two" {
		t.Errorf("second event = %q, want %q", second, "two")
	}

	cancel()
	if err := <-watched; err != nil {
		t.Errorf("watch ended with %v, want a clean stop", err)
	}
}

// And a watch really ends on the provider's side: it is sitting in a Watch that
// returns when it is told to, so a reader that walks away without saying so
// would leave the stream running for the life of the process.
func TestStoppingAWatchReachesTheProvider(t *testing.T) {
	client, _, _ := subprocessClient(t, Config{}, "serve")
	widget := handlerFor(t, client, "Widget")

	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	err := widget.(core.Watcher).Watch(ctx, func(core.Event) error {
		seen++
		if seen == 1 {
			cancel()
		}
		return nil
	})
	cancel()
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// The provider is still there and still answering, which is what proves the
	// stream was closed rather than the connection.
	if _, err := widget.Get(context.Background(), "one"); err != nil {
		t.Errorf("the provider stopped answering after a watch was stopped: %v", err)
	}
}
