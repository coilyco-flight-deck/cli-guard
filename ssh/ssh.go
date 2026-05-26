// Package ssh is the Go-SDK boundary for ssh and scp. Per the cli-guard
// SECURITY.md discipline, simple-API tools (ssh, scp, tailscale) talk to
package ssh

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// DefaultDialTimeout is the TCP+SSH-handshake deadline applied when the
// caller's context has no deadline of its own.
const DefaultDialTimeout = 30 * time.Second

// DefaultPort is the standard ssh port. Used when host has no ":port"
// suffix.
const DefaultPort = "22"

// ErrNoAuth is returned when neither ssh-agent nor a private key file
// can supply a usable signer. Message names the recovery path per
var ErrNoAuth = errors.New("ssh: no authentication method available (ssh-agent unreachable and no key path). Recovery: run `ssh-add ~/.ssh/<key>` to load a key into the agent, or set ssh.key_path in coily config")

// ErrNoKnownHosts is returned when ~/.ssh/known_hosts cannot be read.
// Failing closed here is the point. ssh.InsecureIgnoreHostKey is never
var ErrNoKnownHosts = errors.New("ssh: cannot load known_hosts; refusing to dial without host-key verification")

// Client is a configured ssh dialer. Build one per-process and reuse.
// Nil fields take their default values described on each field.
type Client struct {
	// KeyPath is a path to a PEM-encoded private key. If empty, the
	// running ssh-agent (SSH_AUTH_SOCK) is used.
	KeyPath string

	// KnownHostsPath overrides ~/.ssh/known_hosts. Mainly for tests.
	KnownHostsPath string

	// DialTimeout overrides DefaultDialTimeout. Used only when the
	// caller's context has no deadline.
	DialTimeout time.Duration
}

// Run opens a session on host as user, runs cmd, and returns its stdout
// and stderr. The remote command is passed as a single string to the
func (c *Client) Run(ctx context.Context, host, user, cmd string) (stdout, stderr string, err error) {
	conn, err := c.dial(ctx, host, user)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = conn.Close() }()

	sess, err := conn.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("ssh: new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err := <-done:
		return outBuf.String(), errBuf.String(), err
	case <-ctx.Done():
		// Best-effort cancellation. Closing the session aborts Run.
		_ = sess.Close()
		<-done
		return outBuf.String(), errBuf.String(), ctx.Err()
	}
}

// Stream runs cmd on host as user, streaming stdout/stderr to the
// supplied writers as bytes arrive. Useful for `journalctl -f` style
func (c *Client) Stream(ctx context.Context, host, user, cmd string, stdout, stderr writer) error {
	conn, err := c.dial(ctx, host, user)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	sess, err := conn.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	if stdout != nil {
		sess.Stdout = stdout
	}
	if stderr != nil {
		sess.Stderr = stderr
	}

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = sess.Close()
		<-done
		return ctx.Err()
	}
}

// writer is the io.Writer subset used by StreamWriters. Exported as an
// unnamed interface so callers can pass *os.File or *bytes.Buffer
type writer interface {
	Write(p []byte) (int, error)
}

// StreamStdin is Stream with a stdin reader attached to the remote
// session. Used by `coily ssh deploy` to pipe a sudo password into a
func (c *Client) StreamStdin(ctx context.Context, host, user, cmd string, stdin io.Reader, stdout, stderr writer) error {
	conn, err := c.dial(ctx, host, user)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	sess, err := conn.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	if stdin != nil {
		sess.Stdin = stdin
	}
	if stdout != nil {
		sess.Stdout = stdout
	}
	if stderr != nil {
		sess.Stderr = stderr
	}

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = sess.Close()
		<-done
		return ctx.Err()
	}
}

// CopyTo uploads localPath to remotePath on host as user, using SFTP over
// the same ssh transport as Run / Stream. Host-key verification and auth
func (c *Client) CopyTo(ctx context.Context, host, user, localPath, remotePath string) error {
	conn, err := c.dial(ctx, host, user)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	client, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("ssh: sftp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	src, err := os.Open(localPath) // #nosec G304 -- caller-supplied source path is the point of the API
	if err != nil {
		return fmt.Errorf("ssh: open local %s: %w", localPath, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := client.Create(remotePath)
	if err != nil {
		return fmt.Errorf("ssh: create remote %s: %w", remotePath, err)
	}

	done := make(chan error, 1)
	go func() {
		_, cerr := io.Copy(dst, src)
		if closeErr := dst.Close(); cerr == nil {
			cerr = closeErr
		}
		done <- cerr
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("ssh: copy to %s: %w", remotePath, err)
		}
		return nil
	case <-ctx.Done():
		_ = dst.Close()
		<-done
		return ctx.Err()
	}
}

// dial resolves auth + host-key verification and opens a connection.
func (c *Client) dial(ctx context.Context, host, user string) (*ssh.Client, error) {
	if user == "" {
		return nil, errors.New("ssh: user is empty")
	}
	if host == "" {
		return nil, errors.New("ssh: host is empty")
	}

	auth, err := c.authMethods()
	if err != nil {
		return nil, err
	}

	hkCallback, err := c.hostKeyCallback()
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hkCallback,
		Timeout:         c.dialTimeout(ctx),
		// Restrict the client's host-key-algorithm list to what is actually
		// recorded in known_hosts for this host. Without this, crypto/ssh
		HostKeyAlgorithms: c.knownHostAlgos(host),
	}

	addr := withDefaultPort(host)

	// Use a Dialer that respects ctx for the TCP step. crypto/ssh has no
	// context-aware Dial, so we connect manually then hand the conn to
	d := net.Dialer{Timeout: cfg.Timeout}
	tcp, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, translateDialError(addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(tcp, addr, cfg)
	if err != nil {
		_ = tcp.Close()
		return nil, translateHandshakeError(addr, err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// translateDialError wraps a TCP dial failure with an actionable hint
// when the underlying error matches a common shape. Issue #62: the raw
func translateDialError(addr string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("ssh: tcp dial %s: %w. Recovery: DNS lookup failed; check tailscale connection or /etc/hosts entry for the host", addr, err)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("ssh: tcp dial %s: %w. Recovery: remote port reachable but sshd is not listening; check `sudo systemctl status ssh` on the remote", addr, err)
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded"):
		return fmt.Errorf("ssh: tcp dial %s: %w. Recovery: host unreachable within timeout; check tailscale connection (`tailscale status`) or VPN", addr, err)
	case strings.Contains(msg, "network is unreachable"):
		return fmt.Errorf("ssh: tcp dial %s: %w. Recovery: no network route to host; check tailscale or local network", addr, err)
	}
	return fmt.Errorf("ssh: tcp dial %s: %w", addr, err)
}

// translateHandshakeError wraps an SSH handshake failure with an
// actionable hint. The most common shape on Kai's hosts is auth
func translateHandshakeError(addr string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "no supported methods remain"):
		return fmt.Errorf("ssh: handshake %s: %w. Recovery: server rejected every offered key; verify the right key is loaded with `ssh-add -l`, or set ssh.key_path in coily config", addr, err)
	case strings.Contains(msg, "key mismatch") || strings.Contains(msg, "host key"):
		return fmt.Errorf("ssh: handshake %s: %w. Recovery: known_hosts entry does not match the server's offered key; if the host genuinely changed, remove the stale line with `ssh-keygen -R %s`", addr, err, hostFromAddr(addr))
	}
	return fmt.Errorf("ssh: handshake %s: %w", addr, err)
}

// hostFromAddr strips the :port suffix so a Recovery hint can name the
// `ssh-keygen -R <host>` form the operator actually types.
func hostFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && !strings.Contains(addr, "]") {
		return addr[:i]
	}
	return addr
}

func (c *Client) dialTimeout(ctx context.Context) time.Duration {
	if c.DialTimeout > 0 {
		return c.DialTimeout
	}
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 {
			return d
		}
	}
	return DefaultDialTimeout
}

// authMethods returns the auth list. Order: explicit KeyPath first if
// set, else ssh-agent. Returns ErrNoAuth if nothing usable is available.
func (c *Client) authMethods() ([]ssh.AuthMethod, error) {
	if c.KeyPath != "" {
		signer, err := loadSigner(c.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: load key %s: %w", c.KeyPath, err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, ErrNoAuth
	}
	conn, err := net.Dial("unix", sock) //nolint:gosec // SSH_AUTH_SOCK is the user's own agent
	if err != nil {
		return nil, fmt.Errorf("ssh: dial agent %s: %w", sock, err)
	}
	ag := agent.NewClient(conn)
	signers, err := ag.Signers()
	if err != nil {
		return nil, fmt.Errorf("ssh: agent signers: %w", err)
	}
	if len(signers) == 0 {
		return nil, ErrNoAuth
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, nil
}

// hostKeyCallback wires ~/.ssh/known_hosts (or KnownHostsPath if set)
// into the dial. Always strict. ssh.InsecureIgnoreHostKey is forbidden
func (c *Client) hostKeyCallback() (ssh.HostKeyCallback, error) {
	path := c.KnownHostsPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoKnownHosts, err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoKnownHosts, err)
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoKnownHosts, err)
	}
	return cb, nil
}

// knownHostAlgos parses known_hosts and returns the set of host-key
func (c *Client) knownHostAlgos(host string) []string { //nolint:gocognit,gocyclo,cyclop // known_hosts parsing inherently branches on comment/blank/hashed/hostlist/base64 sanity.
	path := c.KnownHostsPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	// Strip :port if present. knownhosts hashes also start with "|1|"; we
	// don't resolve those here, so hashed entries won't contribute. Kai's
	bare := host
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		bare = host[:i]
	}

	seen := map[string]struct{}{}
	var algos []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// fields[0] is hostlist, fields[1] is algo, fields[2] is base64 key.
		// hostlist may be a comma-separated set. We match exactly; hashed
		hosts := strings.Split(fields[0], ",")
		match := false
		for _, h := range hosts {
			if h == bare {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		// Sanity-check the base64 blob so a malformed line can't inject
		// an algo name that isn't actually backed by a recorded key.
		if _, err := base64.StdEncoding.DecodeString(fields[2]); err != nil {
			continue
		}
		if _, dup := seen[fields[1]]; dup {
			continue
		}
		seen[fields[1]] = struct{}{}
		algos = append(algos, fields[1])
	}
	return algos
}

func loadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}

// withDefaultPort returns host as-is if it already has a ":port" suffix,
// otherwise appends DefaultPort. IPv6 literals (which contain colons)
func withDefaultPort(host string) string {
	if strings.Contains(host, "]") || strings.Count(host, ":") == 1 {
		return host
	}
	return net.JoinHostPort(host, DefaultPort)
}
