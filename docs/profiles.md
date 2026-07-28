# Lockdown profiles

Per-host lockdown profile registry, embedded as `cli/profiles/default.yaml` and copied into the consumer's per-host config (`~/<app-dir>/<app>.yaml`) by `lockdown init-config`.

Each profile is a self-contained tier-per-axis coordinate. There is no inheritance: every profile lists all four axes, every time, and the loader rejects partial entries on purpose. With no override file present, the loader resolves every profile to the strictest tier on every axis (the headless-default) and refuses anything not explicitly allowed. The override file is how the operator opts into looser tiers, per axis, on disk.

## Axes (strictness-ascending, from cli-guard/profile)

- **data_security** - low, medium, high, max
- **blast_radius** - high, medium, low
- **network_egress** - open, allowlisted, loopback-only, air-gapped
- **filesystem_reach** - unrestricted, repo-plus-home, repo-only

## Profiles

Tiers below read `data_security / blast_radius / network_egress / filesystem_reach`.

- **mobile** - Claude Code on Kai's Android app dispatching to kai-server. Public, observable screen and overhearable dictation, poor mobile usability so automation runs high within bounds. `high / medium / allowlisted / repo-plus-home`.
- **mac-tower** - Mac laptop at home: single trusted user and screen, home network, full-disk encryption, ssh-agent loaded. Heads-down engineering, the strongest data-safety env, trusted to run destructive ops with intent. `medium / high / open / unrestricted`.
- **windows-laptop** - Windows desktop at home: isolated and trusted, but Claude on Windows is rough (path mangling, MSYS edges, sandbox quirks), so automation is lower than mac-tower and reach is bounded to dodge drive-letter and UNC surprises. `medium / medium / open / repo-plus-home`.
- **web** - Chrome claude.ai/code, usually public. Assume observable screen, untrusted network, and no safe permission prompts (the agent STOPs and reports rather than escalate, per kai-execution-mode-web). `max / low / allowlisted / repo-only`.
- **headless** - Lights-out autonomous runs, no human watching the output stream. Every axis locked; egress ratchets one notch past web to air-gapped since outbound packets are the primary exfil channel and no operator can authorize a one-off. A headless run needing network fails loud and asks. `max / low / air-gapped / repo-only`.
