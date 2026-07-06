# broker - root credential broker policy core

`pkg/broker` is the policy core for a **root credential broker**: a small,
versioned request/response protocol over a unix socket through which an
unprivileged client asks a privileged server to act on issues and dispatch
work - *without the client ever holding the credential*.

It is the foundation piece for ward's root broker (ward#329), where a root
daemon holds the bot token while an explore agent reaches the forge and the
dispatch path through the daemon socket, holding nothing itself.

## Shape

The package is deliberately self-contained. It carries **no git, docker, or
ward-kdl knowledge**, so it is importable by ward without a dependency cycle:

- **Protocol** (`protocol.go`) - `ProtocolVersion`, the five write-tier `Op`s
  (file / edit / comment / label issue, dispatch), and the `Request` /
  `Response` / `Target` / `Result` wire types. The label op carries a
  `LabelMode` (`add` / `set` / `remove`) and a `Labels` list (names or ids).
  The wire format is newline-delimited JSON: one request in, one response out,
  then the connection closes.
- **Authorizer** (`authz.go`) - the write-tier authorization check, run on
  every request before execution. `Policy` is the default: an owner allowlist
  crossed with the op allowlist, plus the structural invariants every op needs
  (known op, owner+repo present, positive number where required, and a known
  label mode with at least one label for the label op).
- **Executor** (`executor.go`) - the injected privileged side. The server holds
  no token; it authorizes a request and delegates execution to the consumer's
  `Executor`, which holds the credential and talks to the forge / dispatch
  backend.
- **Server** (`server.go`) - listens on an **already-created, already-
  permissioned** unix socket (socket creation and filesystem policy are the
  caller's job), serving one request per connection. It fails closed: a nil
  executor or authorizer is a construction error, and an unknown protocol
  version or unknown op is refused, never guessed.
- **Client** (`client.go`) - the unprivileged dial-once-per-call side, with
  per-op convenience wrappers. It auto-stamps `ProtocolVersion`.

## Why versioned and minimal

This is a high-risk live-dispatch path: a refusal that silently slips through
would hand an unprivileged caller a privileged action. So the surface is kept
minimal and the version is checked rather than negotiated - a mismatch is a
hard refusal, leaving the protocol free to evolve without a fail-open seam.

## Companion: credseed

`pkg/credseed` is the typed surface for seeding a child container's
credentials through a private (`0600`) env-file - the forgejo token plus
optional base64'd agent credential blobs, one line each. It centralizes the
env-var names both the writer (seed) and reader (in-container bootstrap)
depend on, so the two sides cannot drift. It carries only what the broker
needs; full `dispatch.Dispatcher` convergence is out of scope.
