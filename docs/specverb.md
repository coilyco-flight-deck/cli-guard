# spec-driven verbs (guardfile + specverb)

The spec-driven verb subsystem replaces hand-rolled per-verb CLI wrappers with one generic engine that builds the guarded command tree at runtime from an embedded API spec plus a policy.

Three layers:

- **L0 - upstream spec.** The vendor's API truth, embedded. A Swagger 2.0 or OpenAPI 3.0 / 3.1 document (JSON or YAML).
- **L1 - policy IR.** The compiled operation set. A grant's verb+resource resolve to an operation by convention; an explicit `op` overrides. No API-specific table in the engine.
- **L2 - KDL Guardfile.** The human authoring layer. Pure data, parsed never evaluated, compiling to L1.

The engine carries no upstream knowledge, so one engine drives every spec, no code changes.

## guardfile (L2)

`guardfile.Parse` turns a KDL Guardfile into a typed model (group, auth, grants, restrictions, actions). KDL is parsed, never evaluated. The grant's verb+resource ARE the CLI leaf+group, and they resolve to an operation by convention - an explicit `op` is the override seam, not a required binding:

```kdl
wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    base-url "forgejo.coilysiren.me/api/v1"
    auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }

    can get repo                          // convention: GET /repos/{owner}/{repo}
    can list repo { op "repoSearch" }     // irregular: pin it
    can close issue { op "issueEditIssue"; body state="closed" }
}
```

Grant-body nodes: `op "<operationId>"` (optional override; resolution rules in [specverb-resolution.md](specverb-resolution.md)), `body k=v` fixed-body toggles (KDL-native typed values, mount no body flags), `message "..."` (the teaching error a deny surfaces), and `describe "..."`. Quotes are quarantined to the header and these string values. The parser fails closed on unknown nodes, missing required fields, and unsupported auth schemes. Built on `calico32/kdl-go`.

The auth schemes (header-token, bearer, query-param dual-secret), the deny semantics (a deny beats an allow), and the restrict scope gate live in [specverb-policy.md](specverb-policy.md).

## specverb (engine)

`specverb.Build(Config)` assembles the guarded `*cli.Command` tree:

1. Parse the embedded spec into one `kin-openapi` `openapi3.T` IR (Swagger 2.0 upgraded via `openapi2conv`) that resolves `$ref`s, reads `requestBody.content`, promotes `in:query`/`in:path` params, and collapses 3.1 type-lists.
2. For each `can` grant, resolve its operation (by convention or `op`) to a `{method, path, params, body}` descriptor; resource is the CLI group, verb the leaf. **Deny-by-default: an unresolvable or ambiguous grant, or an op the spec lacks, fails closed.**
3. Mount each op as a guarded leaf under `verb.Wrap` (audit + argv gate). A reserved-flag collision is fail-closed; the restrict gate runs at invocation.

One generic action backs every verb: path params positional, query/body fields as typed flags, `--body-file`, fixed-body toggles, injected-resolver auth, `--dry-run`, the `respfmt` render rail - see [specverb-request.md](specverb-request.md).

`specverb.Mount(root, Config)` grafts the built group onto root, generating the intermediate path groups the `wrap` line names. `codegen.Render` generates a consumer's whole `main.go` from the Guardfile (AWS SDK kept out of cli-guard); the no-code `kdl-specs` driver wraps it in a `gen` / `lock` / `skew` / `run` surface, see [kdl-specs.md](kdl-specs.md).

## Spec durability

Proven across three specs: Forgejo (Swagger 2.0 JSON), Trello (OpenAPI 3.0 JSON, `in:query` mutation fields), and Tailscale (OpenAPI 3.1 YAML, `$ref` path params). `Prune` has a path per version, reducing a document to the granted ops plus the transitive closure of the components they reach.

Design: the Forgejo and Trello specverb implementations.
