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

1. Parse the embedded Swagger 2.0 spec (minimal reader: method, path, operationId, path params, scalar query params, body scalars and arrays, one-hop `$ref`).
2. For each `can` grant, resolve `(verb, resource)` through the committed expansion table to `{cliGroup, cliLeaf, operationId}` (plus an optional fixed body). **Deny-by-default: no row, no mount.** The set of rows is the allowlist.
3. Mount each resolved op as a guarded leaf under `verb.Wrap` (audit + argv gate). An unresolvable grant is a fail-closed error, never a silently dropped verb; so is a spec input colliding with a reserved engine flag.

One generic action backs every verb: path params positional, query params and body fields as typed flags, `--body-file`, fixed-body state toggles, injected-resolver auth, `--dry-run`, and the `respfmt` render rail - the full input and firing semantics live in [specverb-request.md](specverb-request.md).

`specverb.Mount(root, Config)` is the consumer entry point: it `Build`s the group and grafts it onto root, generating the intermediate path groups the `wrap` line names (`wrap ward ops forgejo` -> find-or-create `ops`), so a consumer registers the whole surface in one call. Two defaults keep it thin: a scheme-less `base-url` defaults to `https://`, and a nil `HTTPClient` refuses redirects for mutating methods (net/http would otherwise downgrade a redirected POST, dropping the body).

`specgen.Render` generates a consumer's whole `main.go` from the Guardfile (audit `Wrap`, SSM resolver, `Mount`), so the consumer declares only the `.kdl`, with the AWS SDK kept out of cli-guard. The no-code `specverb-gen` driver wraps this in a uv-style `gen` / `lock` / `skew` / `run` surface - see [specverb-driver.md](specverb-driver.md).

## Milestone status

Mounted today: the forgejo repo/org/label/milestone/issue/release/pull/task groups, exercising every shape above. Unit-tested over tree shape, deny-by-default, the Swagger-2.0 gate, dry-run, live create/delete, and `verb.Wrap`.

Named follow-ups (not silent gaps): **M2** `--yes` destructive-confirm + teaching errors; **M4** migrate coily's remaining verbs, prune the spec lock to granted ops.

Two shapes dissolved without new machinery: issue-label verbs need no name->id pre-flight (IssueLabelsOption takes names directly), and release-asset upload is a formData promotion (see [specverb-request.md](specverb-request.md)).

Design: [#75](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/75).
