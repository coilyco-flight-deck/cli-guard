package audit

import (
	"context"
	"os"
)

// SessionIDEnv is the environment variable consulted as the fallback source
// for Record.SessionID when no context value is present. Parent processes
// that spawn coily (or any cli-guard consumer) inject this so subprocess
// flows can join their audit rows to the originating Claude Code session.
// Long-lived in-process servers should prefer WithSessionID instead.
const SessionIDEnv = "CLAUDE_SESSION_ID"

// sessionIDKey is the unexported context key for the session id. Use
// WithSessionID to attach and SessionIDFromContext to read.
type sessionIDKey struct{}

// WithSessionID returns a copy of ctx carrying id as the audit session id.
// Empty id is a no-op (returns ctx unchanged) so callers can safely pass
// through a possibly-unset value without polluting the context.
func WithSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionIDFromContext returns the session id attached to ctx via
// WithSessionID, or empty string if none is set. Nil-safe.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(sessionIDKey{}).(string)
	return v
}

// resolveSessionID applies the documented resolution order: context value
// wins, env var fallback, empty string otherwise. Exposed via Wrap/WrapHook
// which pass the call-site ctx through.
func resolveSessionID(ctx context.Context) string {
	if id := SessionIDFromContext(ctx); id != "" {
		return id
	}
	return os.Getenv(SessionIDEnv)
}
