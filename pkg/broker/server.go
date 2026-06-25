package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
)

// errUnknownOp reports an operation the Server cannot route, worded identically
// by the authz default branch and the execute backstop.
func errUnknownOp(op Op) error {
	return fmt.Errorf("broker: unknown operation %q", op)
}

// Server serves one [Request] per connection on an already-permissioned unix
// socket: authorize, delegate to [Executor], reply. See docs/broker.md.
type Server struct {
	ln   net.Listener
	exec Executor
	auth Authorizer
	// logf, if set, receives one best-effort audit line per served request.
	logf func(format string, args ...any)
}

// NewServer builds a Server over ln. All three deps are required; NewServer
// errors rather than constructing a Server that would fail open.
func NewServer(ln net.Listener, exec Executor, auth Authorizer) (*Server, error) {
	if ln == nil {
		return nil, errors.New("broker: nil listener")
	}
	if exec == nil {
		return nil, errors.New("broker: nil executor")
	}
	if auth == nil {
		return nil, errors.New("broker: nil authorizer")
	}
	return &Server{ln: ln, exec: exec, auth: auth}, nil
}

// SetLogf installs a best-effort audit logger called once per served request;
// pass nil to disable. Not safe to call concurrently with Serve.
func (s *Server) SetLogf(logf func(format string, args ...any)) {
	s.logf = logf
}

// Serve accepts connections until ctx is cancelled or the listener closes,
// handling each in a goroutine. Clean shutdown returns ctx.Err().
func (s *Server) Serve(ctx context.Context) error {
	// Closing the listener unblocks Accept when ctx is cancelled.
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// A cancelled ctx closed the listener: clean shutdown, not a fault.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("broker: accept: %w", err)
		}
		go s.handleConn(ctx, conn)
	}
}

// handleConn serves exactly one request/response exchange on conn, then closes
// it. Every failure path still writes a [Response] so the client never hangs.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	req, err := readRequest(conn)
	if err != nil {
		s.reply(conn, Response{Error: fmt.Sprintf("broker: read request: %v", err)})
		return
	}
	if req.Version != ProtocolVersion {
		s.reply(conn, Response{Error: fmt.Sprintf("broker: unsupported protocol version %d (want %d)", req.Version, ProtocolVersion)})
		return
	}
	if err := s.auth.Authorize(ctx, req); err != nil {
		s.log("DENY op=%s target=%s/%s#%d: %v", req.Op, req.Target.Owner, req.Target.Repo, req.Target.Number, err)
		s.reply(conn, Response{Error: err.Error()})
		return
	}
	res, err := execute(ctx, s.exec, req)
	if err != nil {
		s.log("FAIL op=%s target=%s/%s#%d: %v", req.Op, req.Target.Owner, req.Target.Repo, req.Target.Number, err)
		s.reply(conn, Response{Error: err.Error()})
		return
	}
	s.log("OK op=%s target=%s/%s#%d", req.Op, req.Target.Owner, req.Target.Repo, req.Target.Number)
	s.reply(conn, Response{OK: true, Result: res})
}

// log emits one best-effort audit line when a logger is installed.
func (s *Server) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// reply writes resp to conn, swallowing the write error: the connection is
// closing and there is no second channel to report it on.
func (s *Server) reply(conn net.Conn, resp Response) {
	_ = writeResponse(conn, resp)
}

// readRequest decodes one newline-delimited JSON [Request] from r.
func readRequest(r net.Conn) (Request, error) {
	var req Request
	line, err := bufio.NewReader(r).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Request{}, err
	}
	if uerr := json.Unmarshal(line, &req); uerr != nil {
		return Request{}, uerr
	}
	return req, nil
}

// readResponse decodes one newline-delimited JSON [Response] from r.
func readResponse(r net.Conn) (Response, error) {
	var resp Response
	line, err := bufio.NewReader(r).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Response{}, err
	}
	if uerr := json.Unmarshal(line, &resp); uerr != nil {
		return Response{}, uerr
	}
	return resp, nil
}

// writeResponse encodes resp as one newline-delimited JSON line to w.
func writeResponse(w net.Conn, resp Response) error {
	return writeJSONLine(w, resp)
}

// writeRequest encodes req as one newline-delimited JSON line to w.
func writeRequest(w net.Conn, req Request) error {
	return writeJSONLine(w, req)
}

// writeJSONLine marshals v and writes it as one newline-terminated frame.
func writeJSONLine(w net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
