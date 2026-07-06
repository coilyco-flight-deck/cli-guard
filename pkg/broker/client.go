package broker

import (
	"context"
	"fmt"
	"net"
)

// Client is the unprivileged side of the broker: it dials the Server's unix
// socket, sends one [Request], and returns the [Response]. It holds no token.
type Client struct {
	// SocketPath is the unix socket the Server listens on.
	SocketPath string
}

// NewClient builds a Client for the socket at path.
func NewClient(path string) *Client {
	return &Client{SocketPath: path}
}

// Do sends req over a fresh connection (stamping [ProtocolVersion]) and returns
// the [Response]. A transport failure is the error; a refusal is Response.Error.
func (c *Client) Do(ctx context.Context, req Request) (Response, error) {
	req.Version = ProtocolVersion

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return Response{}, fmt.Errorf("broker: dial %s: %w", c.SocketPath, err)
	}
	defer func() { _ = conn.Close() }()

	// Honor ctx cancellation for the request/response exchange too.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if werr := writeRequest(conn, req); werr != nil {
		return Response{}, fmt.Errorf("broker: write request: %w", werr)
	}
	resp, rerr := readResponse(conn)
	if rerr != nil {
		return Response{}, fmt.Errorf("broker: read response: %w", rerr)
	}
	return resp, nil
}

// FileIssue is a convenience wrapper for an [OpFileIssue] request.
func (c *Client) FileIssue(ctx context.Context, target Target, title, body string) (Response, error) {
	return c.Do(ctx, Request{Op: OpFileIssue, Target: target, Title: title, Body: body})
}

// EditIssue is a convenience wrapper for an [OpEditIssue] request.
func (c *Client) EditIssue(ctx context.Context, target Target, title, body, state string) (Response, error) {
	return c.Do(ctx, Request{Op: OpEditIssue, Target: target, Title: title, Body: body, State: state})
}

// CommentIssue is a convenience wrapper for an [OpCommentIssue] request.
func (c *Client) CommentIssue(ctx context.Context, target Target, body string) (Response, error) {
	return c.Do(ctx, Request{Op: OpCommentIssue, Target: target, Body: body})
}

// LabelIssue is a convenience wrapper for an [OpLabelIssue] request. mode is
// [LabelAdd], [LabelSet], or [LabelRemove]; labels names the labels to act on.
func (c *Client) LabelIssue(ctx context.Context, target Target, mode string, labels []string) (Response, error) {
	return c.Do(ctx, Request{Op: OpLabelIssue, Target: target, LabelMode: mode, Labels: labels})
}

// Dispatch is a convenience wrapper for an [OpDispatch] request.
func (c *Client) Dispatch(ctx context.Context, target Target) (Response, error) {
	return c.Do(ctx, Request{Op: OpDispatch, Target: target})
}
