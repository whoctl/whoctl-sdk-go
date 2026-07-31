package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Loopback is a Transport that serves a provider in this process, with the
// requests and responses really encoded to JSON and decoded back.
//
// The encoding is the point. A transport that passed Go values straight through
// would prove nothing: what has to be true is that everything a provider says
// survives being written as JSON and read back — field order, numbers that are
// not floats, a nil spec staying absent, an error keeping its code. Running the
// existing suites through this is how that gets checked before anything is
// moved into a separate process, where the same bug would look like a protocol
// failure instead of a serialization one.
type Loopback struct{ server *Server }

// NewLoopback wraps a server as a transport.
func NewLoopback(s *Server) *Loopback { return &Loopback{server: s} }

// Call implements Transport.
func (l *Loopback) Call(ctx context.Context, req Request) (Response, error) {
	wire, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if bytes.ContainsAny(wire, "\n") {
		// The stdio transport frames one request per line, so a newline inside
		// an encoded request would desynchronize it. encoding/json escapes
		// them, and this is here to notice if that ever stops being true.
		return Response{}, fmt.Errorf("encoded request contains a newline")
	}

	var decoded Request
	if err := json.Unmarshal(wire, &decoded); err != nil {
		return Response{}, err
	}

	resp := l.server.Handle(ctx, decoded)

	encoded, err := json.Marshal(resp)
	if err != nil {
		return Response{}, err
	}
	var out Response
	if err := json.Unmarshal(encoded, &out); err != nil {
		return Response{}, err
	}
	return out, nil
}

// Serve connects a client to a provider through a loopback transport. It is
// what registers a provider today: in process, but only reachable through the
// protocol.
func Serve(ctx context.Context, s *Server, cfg Config) (*Client, error) {
	return Connect(ctx, NewLoopback(s), cfg)
}
