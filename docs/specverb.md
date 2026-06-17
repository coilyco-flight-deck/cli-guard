# spec-driven verbs (guardfile + specverb)

The spec-driven verb subsystem replaces hand-rolled per-verb CLI wrappers with one generic engine that builds the guarded command tree at runtime from an embedded API spec plus a human-authored policy.

Three layers:

- **L0 - upstream spec.** The vendor's API truth, embedded. A Swagger 2.0 or OpenAPI 3.0 / 3.1 document (JSON or YAML).
- **L1 - policy IR.** The compiled operation set, each grant resolved by its own `op` binding (no engine-resident expansion table).
- **L2 - KDL Guardfile.** The human authoring layer. Pure data, parsed never evaluated, compiling to L1.

The engine carries no upstream knowledge: a grant's `op` is the only bridge from policy to spec, so one engine drives every spec without code changes.

## guardfile (L2)

`guardfile.Parse` turns a KDL Guardfile into a typed model (group, auth, grants, restrictions, actions). KDL is parsed, never evaluated. The grant's verb+resource ARE the CLI leaf+group, and a grant-body `op` binds that placement to an operationId:

```kdl
wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    base-url "forgejo.coilysiren.me/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        ssm "/forgejo/api-token"
    }

    restrict owner matches "coily*"

    can get repo { op "repoGet" }
    can create repo { op "createCurrentUserRepo" }
    can close issue { op "issueEditIssue"; body state="closed" }
    never delete repos { message "repo deletion is irreversible; archive instead" }
}
```

Grant-body nodes: `op "<operationId>"` (required on `can`), `body k=v` fixed-body toggles (KDL-native typed values, mount no body flags), `message "..."` (the teaching error a deny surfaces), and `describe "..."`. Quotes are quarantined to the header and these string values. The parser fails closed on unknown nodes, missing required fields, and unsupported auth schemes. Built on `calico32/kdl-go`.

The auth schemes (header-token, bearer, query-param dual-secret), the deny semantics (a deny beats an allow), and the restrict scope gate live in [specverb-policy.md](specverb-policy.md).

## specverb (engine)

`specverb.Build(Config)` assembles the guarded `*cli.Command` tree:

1. Parse the embedded spec, dispatching on version: a Swagger 2.0 reader, or an OpenAPI 3.x reader (via `kin-openapi`) that resolves `components` `$ref`s, reads `requestBody.content`, promotes `in:query`/`in:path` params, and collapses 3.1 type-lists.
2. For each `can` grant, resolve its `op` to a `{method, path, params, body}` descriptor; resource is the CLI group, verb the leaf. **Deny-by-default: no `op`, or an op the spec lacks, is a fail-closed error.**
3. Mount each op as a guarded leaf under `verb.Wrap` (audit + argv gate). A reserved-flag collision is fail-closed; the restrict gate runs at invocation.

One generic action backs every verb: path params positional, query/body fields as typed flags, `--body-file`, fixed-body toggles, injected-resolver auth, `--dry-run`, the `respfmt` render rail - see [specverb-request.md](specverb-request.md).

`specverb.Mount(root, Config)` grafts the built group onto root, generating the intermediate path groups the `wrap` line names. `specgen.Render` generates a consumer's whole `main.go` from the Guardfile (AWS SDK kept out of cli-guard); the no-code `specverb-gen` driver wraps it in a `gen` / `lock` / `skew` / `run` surface, see [specverb-driver.md](specverb-driver.md).

## Spec durability

Proven across three specs: Forgejo (Swagger 2.0 JSON), Trello (OpenAPI 3.0 JSON, mutation fields in `in:query`), and Tailscale (OpenAPI 3.1 YAML, `components/parameters` path-param `$ref`). `Prune` has a path per version, reducing a document to the granted ops plus the transitive closure of the components they reach, idempotent.

Design: [#75](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/75), [#146](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/issues/146).
