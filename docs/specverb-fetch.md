# fetch overlays

`fetch` is the non-Swagger companion to the spec-driven `can` surface. It mounts
fixed HTTP leaves directly from the Guardfile, so a consumer can replace a
cwd-relative shell bridge with a reviewable request shape.

## Grammar

```kdl
wrap ward ops forgejo {
    base-url "https://forgejo.example/api/v1"

    fetch "actions logs" {
        method "GET"
        path "/repos/{owner}/{repo}/actions/runs/{run}/jobs/{job}/attempt/{attempt}/logs"
        output "raw"

        env FORGEJO_TOKEN {
            value ssm "/forgejo/token"
        }

        header "Authorization" "token ${FORGEJO_TOKEN}"
        header "Accept" "text/plain"

        when first input matches coily*
    }
}
```

## Rules

- `method` and `path` are required.
- `path` placeholders become positional arguments in `{placeholder}` order.
- `output` is required and currently must be `raw`.
- `env <name> { value ... }` resolves a template variable through the shared
  value-provider registry.
- `header "<name>" "<template>"` may interpolate `${NAME}` placeholders from the
  declared fetch envs.
- `when first input matches ...` is sugar for `when arg0 matches ...`.
- Unknown nodes and malformed templates fail closed.

## Runtime

- Dry-run prints the resolved request and redacts env-backed header values.
- Live fetches print the raw response body to stdout.
- Non-2xx responses fail closed with the HTTP status and trimmed body in the
  error.
- Redirect handling follows the shared opcore client floor: `GET` and `HEAD`
  may follow, mutating methods refuse silent redirects.

## See also

- [specverb.md](specverb.md) - the wider Guardfile and specverb engine.
- [specverb-request.md](specverb-request.md) - request assembly details.
