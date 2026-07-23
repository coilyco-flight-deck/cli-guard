# install specgen

Each cli-guard tag publishes raw `specgen` binaries for Linux, macOS, and
Windows on amd64 and arm64. Verify the selected binary against the release's
`SHA256SUMS`, rename it to `specgen` (or `specgen.exe`), and place it on
`PATH`.

Go users can install the tagged command directly:

```sh
GOPRIVATE=forgejo.coilysiren.me go install forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cmd/specgen@vX.Y.Z
```

The packaged driver still invokes the Go toolchain for `lock`, `build`, and
`run`. `specgen --version` prints the driver version and the cli-guard module
ref that an unqualified `lock` will freeze. Release binaries and tagged
`go install` builds report the same tag for both values. A source checkout
reports `(devel)` and defaults the lock ref to `latest`.

The legacy `cmd/kdl-specs` Go path remains a temporary compatibility
entrypoint for pinned consumers. New installations use `cmd/specgen`, and
releases publish only `specgen` assets.

## See also

- [specgen.md](specgen.md) - driver lifecycle, discovery, and locks.
- [release-pipeline.md](release-pipeline.md) - packaged artifact publication.
- [FEATURES.md](FEATURES.md) - shipped feature inventory.
