# specverb policy: auth, deny, restrict

The policy surface a Guardfile authors on top of the `op`-bound grants. See [specverb.md](specverb.md) for the engine and layering.

## Auth schemes

Three schemes, named on the `auth` node, each redacting its secret(s) in `--dry-run`:

- `header-token { header; prefix; ssm }` - Forgejo's `Authorization: token <key>`. The trailing space in `prefix "token "` is significant.
- `bearer { ssm }` - Tailscale. Implies the `Authorization` header with a `Bearer ` prefix.
- `query-param { param key { ssm }; param token { ssm } }` - Trello's dual-secret form: each named secret is injected as a query parameter (`?key=&token=`), resolved from its own SSM path.

The request builder resolves the scheme's secret(s) and applies them as a header or query parameters. The describe surface names the scheme and its SSM path(s), never the value.

## Deny: a deny beats an allow

A `cannot`/`never <verb> <resource>` blocks that class. The deny wins over any matching `can` (defense in depth): the allowed leaf is dropped from the mounted tree, the spec lock, and the action poll set, and replaced by a teaching leaf that fails closed with a `PolicyDenied` exit carrying the grant's `message`. A deny over a resource with no allow still mounts its teaching leaf, so an operator who reaches for a blocked verb learns why instead of hitting an "unknown command".

```kdl
never delete repos { message "repo deletion is irreversible; archive instead" }
never create orgs { message "org creation is a human-only operation" }
```

## Restrict: the scope gate

`restrict <param> matches "<glob>"...` is a wrap-level allowlist. Every leaf whose path template carries `{param}` must supply an argument matching at least one glob (filepath.Match-style) at invocation, or it fails closed with a `PolicyDenied` exit before any wire call. A malformed glob matches nothing. Enforced on both the direct leaf path and the action poll/call request path.

```kdl
restrict owner matches "coily*" "coilyco-*"
```

The describe surface documents both the denials ("Denied operations") and the scope restrictions ("Scope restrictions").
