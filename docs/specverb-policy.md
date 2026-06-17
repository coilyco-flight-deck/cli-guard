# specverb policy: auth, deny, restrict

The policy surface a Guardfile authors on top of the `op`-bound grants. See [specverb.md](specverb.md) for the engine and layering.

## Value sources

A secret or opaque host is named, never committed: `value <provider> "<address>"`. The provider names a store; the address is whatever it interprets (an SSM path, a tailnet device name, an env var). cli-guard never reads the store - a registered `specverb.Provider` does, so store clients stay in the consumer. It ships three no-SDK built-ins (`env`, `file`, `literal`); a consumer registers the rest via `Config.Providers` (the generated binary wires `ssm`, `tailscale`). An unregistered provider fails closed at request time, never a silent empty secret; an offline build/describe/mount needs nothing wired.

## Auth schemes

Three schemes, named on the `auth` node, each redacting its secret(s) in `--dry-run`:

- `header-token { header; prefix; value <provider> "addr" }` - Forgejo's `Authorization: token <key>`. The trailing space in `prefix "token "` is significant.
- `bearer { value <provider> "addr" }` - Tailscale. Implies the `Authorization` header with a `Bearer ` prefix.
- `query-param { param key { value <provider> "addr" }; param token { value <provider> "addr" } }` - Trello's dual-secret form: each named secret is injected as a query parameter (`?key=&token=`), read from its own value source.

The request builder resolves the scheme's secret(s) and applies them as a header or query parameters. The describe surface names the scheme and its value source(s) (`provider address`), never the value.

## base-url from a value source

`base-url` takes either a committed string (`base-url "host/api/v1"`) or a block that resolves the host through a value provider at request time:

```kdl
base-url { value ssm "/coilysiren/open-webui/url" }     // opaque host stashed in SSM
base-url { value tailscale "open-webui" }               // tailnet host resolved live
```

The block form exists for a tailnet-only or otherwise opaque host that must not be committed (the `tailscale` provider resolves a device name to its IPv4 live, so no FQDN is stashed). It resolves lazily on the first real request, through the same provider registry as the auth token, and caches the result: mounting the tree (and so `--help`) never touches the store. A `--dry-run` preview stays offline, showing the host symbolically as `{base-url:<provider> <address>}`, and describe names the value source rather than resolving it. The two forms are mutually exclusive, and a spec member must carry one. Because the host is not committed, no spec fetch URL is derivable, so the spec is vendored beside the guardfile (read locally at `lock`).

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
