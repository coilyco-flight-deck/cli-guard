# golangci-lint config notes

Rationale for the non-obvious choices in `.golangci.yaml` (config adopted from coilysiren/coily for the cli-* family; run with `coily exec lint`). It leans on cyclomatic-complexity checks because these packages are security boundaries or wire-protocol layers, where tangled branchy code is where the bugs live.

## gosec exclusions

- **G204** fires on every `exec.CommandContext(ctx, bin, argv...)` even with argv properly constructed. Argv validation happens at the cli-guard policy layer; refusing it here would defeat the point of the wrappers.
- **G301/G302/G304/G306** (file permissions) - perms are managed deliberately per call site, so the per-site choice is trusted over a blanket rule.

## Path-scoped exclusions

- Generated files (`_generated\.go$`) and tests (`_test\.go$`) relax complexity and a few correctness linters: mechanical or long table-driven code is fine.
- Examples are matched on `(^|/)examples/`, not `^examples/`. In a git worktree golangci-lint reports paths prefixed with the relative hop back to the checkout (`../../../cli-guard/examples/...`), which a start-anchored pattern would miss, leaking example-only lint noise into every dispatched commit.
