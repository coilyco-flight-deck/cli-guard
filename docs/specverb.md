# spec-driven verbs (guardfile + specverb)

The spec-driven verb subsystem replaces hand-rolled per-verb CLI wrappers with one generic engine that builds the guarded command tree at runtime from an embedded API spec plus a human-authored policy. Adding a verb becomes a one-sentence edit, not new Go.

Three layers:

- **L0 - upstream spec.** The vendor's API truth (Forgejo's `swagger.v1.json`). Embedded, never live-fetched.
- **L1 - policy IR.** The compiled operation set the runtime mounts from. M0 resolves it in-process from the expansion table; the M1 form is an OpenAPI Overlay carrying `x-cli-guard-*` extensions.
- **L2 - KDL Guardfile.** The human authoring layer. Pure data, parsed never evaluated, compiling to L1.

## guardfile (L2)

`guardfile.Parse` turns a KDL Guardfile into a typed model (`Group`, `Spec`, `BaseURL`, `Auth`, `Grants`). KDL is parsed, never `eval`'d, so a Guardfile carries no executable code into the build - the property that makes it safe for a tool whose job is keeping hostile code from running. The policy body is flat declarative sentences, every grant token a bare KDL identifier:

```kdl
wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    base-url "https://forgejo.coilysiren.me/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        ssm "/forgejo/api-token"
    }

    can read repos
    can create repos
    can delete repos
}
```

Quotes are quarantined to the write-once config header (`base-url`, the trailing-space `prefix "token "`, the `/`-bearing `ssm` path). The parser fails closed on unknown nodes, missing required fields, and unsupported auth schemes. Built on `calico32/kdl-go`.

## specverb (engine)

`specverb.Build(Config)` assembles the guarded `*cli.Command` tree:

1. Parse the embedded Swagger 2.0 spec (minimal M0 reader: method, path, operationId, ordered path params, request-body scalars, one-hop `$ref`).
2. For each `can` grant, resolve `(verb, resource)` through the committed expansion table to `{cliGroup, cliLeaf, operationId}`. **Deny-by-default: no row, no mount.** The set of rows is the M0 allowlist.
3. Mount each resolved op as a guarded leaf under `verb.Wrap` (audit + argv gate). An unresolvable grant is a fail-closed error, never a silently dropped verb.

One generic action backs every verb:

- **Path params** become positional args (`repo delete <owner> <repo>`).
- **Request-body scalars** are promoted to typed flags; required schema field -> required flag, unset optional omitted from the body rather than sent as a zero value.
- **Auth** (header-token) resolves the secret through an injected `TokenResolver`, keeping the AWS SDK out of cli-guard - the consumer wires the real SSM resolver, tests inject a fake.
- **`--dry-run`** prints the resolved request with the secret redacted and fires nothing; live responses render through the `respfmt` `--query`/`--output` rail (an empty 2xx prints a confirmation).

The Guardfile and expansion table resolve to a concrete operation set at build time - a compiler, never an interpreter, no model in the request path - and anything unresolved is a refusal, not a guess.

## Milestone status

M0 (this slice) mounts the forgejo repo read/create/delete proving trio from a fixture spec, `--dry-run` working, unit-tested over tree shape, deny-by-default, the Swagger-2.0 gate, dry-run, live create/delete, arg validation, and `verb.Wrap` composition.

Named follow-ups (not silent gaps):

- **M1** - the Guardfile -> OpenAPI Overlay -> Speakeasy lowering (L1 form); full kin-openapi Swagger-2.0 -> 3.x; ward wiring the real SSM resolver, diffed against coily's `repo create`.
- **M2** - fanatical-UX pass: `--yes` destructive-confirm (the descriptor carries `Destructive`), teaching errors, generated help, flat-sentence lint.
- **M4** - embed + pin the spec by hash; migrate coily's verbs to specverb.

Design + checkpoint: [cli-guard#75](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/75).
