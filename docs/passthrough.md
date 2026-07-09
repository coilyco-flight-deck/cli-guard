# passthrough dialect (execverb)

The passthrough dialect is the default-allow shape of [execverb](execverb.md): a tool wrapped whole rather than verb-by-verb. Where `exec` + `can run` is an allowlist (deny-by-default), `passthrough` opens a `can run "*"` funnel and the wrap-level guards subtract from it. It suits interactive tools where naming every shape is impractical (ssh) but a few things must still be refused.

```kdl
wrap ward-kdl ssh {
    passthrough ssh                                       // exec ssh + an implicit `can run *`
    never pass rm                                         // deny an rm positional over the funnel
    only pass when shell hostname is "*macbook*" "*-laptop" // host gate: workstations only, fail-closed
}
```

## Grammar

- **`passthrough <bin> [prefix...]`** - sugar for `exec <bin>` + an implicit `can run "*"`. The first argument is the binary, the rest is the fixed leading argv: `passthrough ssh` execs `ssh <args...>`, `passthrough tailscale ssh` execs `tailscale ssh <args...>`. Mutually exclusive with `exec` and `can run` (a wrap is an allowlist or a funnel, never both). Accepts an `{ env ... }` body like `exec`.
- **`never pass <token...>`** - wrap-level deny: refuse when any positional matches a token glob. `never pass rm` blocks an `rm` argument.
- **`never pass when <selector> is <glob...>`** - wrap-level deny on a match.
- **`only pass when <selector> is <glob...>`** - wrap-level allow: pass only on a match, fail closed otherwise.

`is` and `matches` are interchangeable glob comparators (filepath.Match, case-insensitive). All three guards take an optional `{ describe "..." }` note and are enforced on every leaf, before any exec.

### Selectors

A guard selector is either an **argv slot** - `any-arg` (all positionals), `argN` (Nth positional), or a flag name - or an ambient **`shell <cmd> <args...>`** source. The shell source execs the command directly (no shell interpretation, so a guardfile cannot inject) once at invocation and matches its trimmed stdout. A resolver error fails the guard closed.

## The host gate

The motivating use is `only pass when shell hostname is <workstation globs>`: the local `hostname` is a value the caller cannot forge, so the gate cleanly answers "may this machine originate the call." Workstations match the allowlist, everything else (servers, CI nodes) fails closed. This is what makes a passthrough safe to grant - the funnel is open, but only from trusted origins, and the host fact is ambient rather than argv-derived.

## Limits

A `never pass <token>` over an opaque-argument tool is a speed-bump, not a boundary. `ssh host rm -rf /` is caught, but `ssh host /bin/rm ...`, `ssh host 'foo; rm ...'`, or an `rm` inside a remote shell are not - ssh runs an opaque string on another box. Real protection for the remote side is the remote host's own controls. The host gate, by contrast, is a genuine boundary: it gates the trusted origin, not the untrusted payload.

## Engine

The wildcard funnel already enforced `when`/`deny-when`/gates/flag-policy at invocation (`actionFor`). The passthrough dialect lifts `when`/`deny-when` to wrap scope (`Guardfile.Whens`, applied to every leaf) and adds the `shell <cmd>` selector source resolved through a `HostResolver` (injectable for tests; nil execs for real). `Surface.Guards` renders the wrap-level guards in the describe doc.

Design: the security-pure-engine refactor.
