# Release pipeline

Forgejo is the canonical and only release surface for cli-guard. GitHub is not
in the loop. cli-guard is the base library of the cli-guard / ward / coily
triple, so its releases drive the other two.

## Flow

- Push to `main` lands on Forgejo.
- `.forgejo/workflows/release.yml` fires:
  - **release** - `tag-bump` reads the conventional commits since the last
    semver tag and computes the next version (`feat` -> minor, `fix` ->
    patch, `!:` or `BREAKING CHANGE` -> major, patch otherwise), creates the
    tag, then `create-release` cuts the Forgejo release. Both writes use the
    auto-issued job token.
  - **cascade** - for each downstream consumer (ward, coily), checks out the
    repo, runs `go get cli-guard@<new-tag>` + `go mod tidy`, pushes a
    `chore/bump-cli-guard-<tag>` branch, and opens a PR. Merging that PR fires
    the downstream's own release. This retires coily's floating cli-guard
    pseudo-version and keeps both consumers pinned to real tags.

cli-guard ships no Homebrew formula: it is a library plus the
`cmd/cli-guard-hook` binary, consumed through `go.mod`, not installed via brew.
So there is no formula-bump job here (ward and coily have those, pointing at
the centralized taps).

## Required secret

- `CI_RELEASE_TOKEN` - a Forgejo PAT with `write:repository` on
  `coilyco-flight-deck/ward` and `coilyco-bridge/coily`. The cascade uses it to
  push the bump branch and open the PR on each downstream. The release + tag
  themselves do not need it (the job token suffices), and the cascade fails
  soft when the secret is absent, so this workflow can land and cut its first
  tag before the secret is provisioned. Set it once in the cli-guard repo
  settings on Forgejo.

## Why a cascade

The triple's dependency direction is cli-guard -> {ward, coily}. Before this
pipeline, cli-guard tags were cut by hand and downstreams floated on
pseudo-versions (coily) or stale tags (ward). Automating the bump means a
cli-guard release propagates to a real, reviewable dependency PR on each
consumer in one step, instead of a manual `go get` per repo.

See [cli-guard release automation](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard).
