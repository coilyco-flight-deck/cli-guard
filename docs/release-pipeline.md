# Release pipeline

Forgejo is the canonical and only release surface for cli-guard. GitHub is not
in the loop. cli-guard is the base library of the cli-guard / ward stack
(coily, the original third member, has been retired).

## Flow

- Push to `main` lands on Forgejo.
- `.forgejo/workflows/release.yml` fires the **release** job: `tag-bump` reads
  the conventional commits since the last semver tag and computes the next
  version (`feat` -> minor, `fix` -> patch, `!:` or `BREAKING CHANGE` ->
  major, patch otherwise), creates the tag, then `create-release` cuts the
  Forgejo release. Both writes use the auto-issued job token, so no cross-repo
  secret is needed.

cli-guard ships no Homebrew formula: it is a library plus the
`cmd/cli-guard-hook` binary, consumed through `go.mod`, not installed via brew.
So there is no formula-bump job here (ward has one, pointing at the
centralized taps).

## Tag-only by design: cli-guard does not bump its consumers

The stack's dependency direction is cli-guard -> ward. cli-guard is
the base, so its automation must not reach up into its consumers. Having
cli-guard open dependency-bump PRs on ward would reverse the
`dependsOn` edge (a dependency mutating its dependents), which couples the
tree backwards.

Downstream bumps belong to the consumers, pulled along the dependency arrow:
ward watches cli-guard's tags and opens its own self-bump PR.
That keeps every cross-repo write pointing from a consumer toward what it
depends on. The tree-direction rule is being made enforceable as a linter in
[agentic-os-kai#626](https://forgejo.coilysiren.me/coilyco-bridge/agentic-os-kai/issues/626).

See [cli-guard release automation](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard).
