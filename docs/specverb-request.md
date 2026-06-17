# specverb request semantics

How the one generic action behind every mounted leaf assembles, previews, and fires its HTTP request. The engine and policy layers are in [specverb.md](specverb.md).

## Inputs

- **Path params** become positional args (`repo delete <owner> <repo>`), count-validated before any wire call.
- **Scalar query params** become typed flags; set ones encode into the URL query string, unset ones are omitted.
- **Body fields** (scalars and arrays of scalars) become typed flags; an unset optional is omitted from the JSON, never sent as a zero value. Arrays repeat the flag (`--assignees a --assignees b`).
- **`--body-file <path>`** supplies the whole JSON body instead, mutually exclusive with body flags. This is the gate-safe spill: file contents bypass the shell-metachar gate (they are never shell-interpolated), while the path itself stays gated.
- **Required body fields** are enforced at request assembly, not the CLI layer, so either source - flags or `--body-file` - satisfies them.
- **State toggles** (`can close issues`) mount fixed-body leaves: the leaf sends exactly the table-declared body (`{"state":"closed"}`) and mounts no body flags.

A promoted spec input that would shadow a reserved engine flag (`--dry-run`, `--query`, `--output`, `--body-file`), or a query/body name collision on one leaf, refuses to build - fail-closed, never silent shadowing.

## Firing

- **Auth** (header-token) resolves the secret through the value-provider registry (`value <provider> "addr"`), keeping the AWS SDK and other store clients out of cli-guard - the consumer registers the real `ssm`/`tailscale` resolvers, tests inject a fake or lean on the `env`/`file`/`literal` built-ins.
- **`--dry-run`** prints the resolved request with the secret redacted and fires nothing.
- Live responses render through the `respfmt` `--query`/`--output` rail; an empty 2xx prints an `ok:` confirmation line.
- The default client refuses redirects for mutating methods, so a renamed or transferred target cannot silently swallow a write.
