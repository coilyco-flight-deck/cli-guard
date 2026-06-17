# specverb policy: auth, deny, restrict

The policy surface a Guardfile authors on top of the `op`-bound grants. See [specverb.md](specverb.md) for the engine and layering.

## Auth schemes

Three schemes, named on the `auth` node, each redacting its secret(s) in `--dry-run`:

- `header-token { header; prefix; ssm }` - Forgejo's `Authorization: token <key>`. The trailing space in `prefix "token "` is significant.
- `bearer { ssm }` - Tailscale. Implies the `Authorization` header with a `Bearer ` prefix.
- `query-param { param key { ssm }; param token { ssm } }` - Trello's dual-secret form: each named secret is injected as a query parameter (`?key=&token=`), resolved from its own SSM path.

The request builder resolves the scheme's secret(s) and applies them as a header or query parameters. The describe surface names the scheme and its SSM path(s), never the value.

## base-url from SSM

`base-url` takes either a committed string (`base-url "host/api/v1"`) or a block that resolves the host from SSM at request time:

```kdl
base-url { ssm "/coilysiren/open-webui/url" }
```

The block form exists for a tailnet-only or otherwise opaque host that must not be committed to a public repo. It resolves lazily, on the first real request, through the same SSM resolver as the auth token, and caches the result: mounting the tree (and so `--help` and any unrelated merged member) never touches AWS. A `--dry-run` preview stays offline like the redacted secret, showing the host symbolically as `{base-url:ssm <path>}`, and the describe surface names the SSM path rather than resolving it. The two forms are mutually exclusive, and a spec member must carry one of them. Because the host is not committed, no spec fetch URL is derivable, so the spec must be vendored beside the guardfile (read locally at `lock`).

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
