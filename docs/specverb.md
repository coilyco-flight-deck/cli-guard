# spec-driven verbs (guardfile + specverb)

The spec-driven verb subsystem replaces hand-rolled per-verb CLI wrappers with one generic engine that builds the guarded command tree at runtime from an embedded API spec plus a human-authored policy. Adding a verb is a one-sentence edit, not new Go.

Three layers:

- **L0 - upstream spec.** The vendor's API truth (Forgejo's `swagger.v1.json`), embedded.
- **L1 - policy IR.** The compiled operation set, resolved from the expansion table.
- **L2 - KDL Guardfile.** The human authoring layer. Pure data, parsed never evaluated, compiling to L1.

## guardfile (L2)

`guardfile.Parse` turns a KDL Guardfile into a typed model (`Group`, `Spec`, `BaseURL`, `Auth`, `Grants`). KDL is parsed, never `eval`'d, so it carries no executable code into the build. The policy body is flat declarative sentences, every grant token a bare KDL identifier:

```kdl
wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    base-url "forgejo.coilysiren.me/api/v1"
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

Quotes are quarantined to the config header (`base-url`, the trailing-space `prefix "token "`, the `ssm` path). The parser fails closed on unknown nodes, missing required fields, and unsupported auth schemes. Built on `calico32/kdl-go`.

## specverb (engine)

`specverb.Build(Config)` assembles the guarded `*cli.Command` tree:

1. Parse the embedded Swagger 2.0 spec (minimal M0 reader: method, path, operationId, ordered path params, request-body scalars, one-hop `$ref`).
2. For each `can` grant, resolve `(verb, resource)` through the committed expansion table to `{cliGroup, cliLeaf, operationId}`. **Deny-by-default: no row, no mount.** The set of rows is the M0 allowlist.
3. Mount each resolved op as a guarded leaf under `verb.Wrap` (audit + argv gate). An unresolvable grant is a fail-closed error, never a silently dropped verb.

One generic action backs every verb:

- **Path params** become positional args (`repo delete <owner> <repo>`).
- **Request-body scalars** are promoted to typed flags; required schema field -> required flag, unset optional omitted rather than sent as a zero value.
- **Auth** (header-token) resolves the secret through an injected `TokenResolver`, keeping the AWS SDK out of cli-guard - the consumer wires the real SSM resolver, tests inject a fake.
- **`--dry-run`** prints the resolved request with the secret redacted and fires nothing; live responses render through the `respfmt` `--query`/`--output` rail.

`specverb.Mount(root, Config)` is the consumer entry point: it `Build`s the group and grafts it onto root, generating the intermediate path groups the `wrap` line names (`wrap ward ops forgejo` -> find-or-create `ops`), so a consumer registers the whole surface in one call. Two defaults keep it thin: a scheme-less `base-url` defaults to `https://`, and a nil `HTTPClient` refuses redirects for mutating methods (net/http would otherwise downgrade a redirected POST, dropping the body).

`specgen.Render` (via `cmd/specverb-gen`) goes further, generating a consumer's whole `main.go` from the Guardfile - spec resolution, audit `Wrap`, SSM resolver, `Mount` - so the consumer declares only the `.kdl`. The generated, gitignored file keeps the AWS SDK in the consumer module, out of cli-guard.

## Milestone status

M0 mounts the forgejo repo read/create/delete trio, unit-tested over tree shape, deny-by-default, the Swagger-2.0 gate, dry-run, live create/delete, and `verb.Wrap`. M1 wires it into ward via `Mount` + the real SSM resolver, diffed against coily's `repo create`.

Named follow-ups (not silent gaps):

- **M2** - fanatical-UX: `--yes` destructive-confirm, teaching errors.
- **M4** - pin the spec by hash; migrate coily's verbs to specverb.

Design: [cli-guard#75](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/75).
