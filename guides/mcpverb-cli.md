# mcpverb-cli

[mcpverb](mcpverb.md) imported the engine and started its own server in the same
process, which made it self-contained and made it a demonstration. This is the
product path: **KDL policy and a committed lock, with no Go at all**, against a
server this repository does not control.

The upstream is `@modelcontextprotocol/server-everything`, the protocol's own
reference implementation, started over stdio by `npx`. It exercises the stdio
transport, which an in-process server cannot.

You need `npx` and a network connection for one step, and nothing after it.
Paths below are shortened, and refusal text and exit codes are verbatim.

## The guardfile

```kdl
description "guarded verbs over the MCP reference server"

wrap mcpdemo ops everything {
    mcp stdio {
        command "npx"
        argv "-y" "@modelcontextprotocol/server-everything"
    }

    can call echo

    can call get-sum {
        describe "add two integers, both bound as typed flags"
    }

    can call get-structured-content {
        fail-when "temperature == null"
    }

    never call get-env
}
```

Fourteen tools upstream. Three granted, one denied by name, and ten named by no
sentence at all.

Nothing here restates an argument shape. `echo` takes a message, `get-sum` takes
two numbers, and the guardfile mentions none of that, because the lock carries
the server's own JSON Schema and the flags are generated from it.

**A stdio upstream starts a subprocess**, so `command` and `argv` go through the
same shell-metacharacter gate every other umbra exec passes. A spawn does not
get a weaker gate for arriving through the request surface. Secrets for one go
in an `env` block rather than `argv`, because argv is readable by any local
process.

## Lock it

This is the online step, and the only one.

```sh
umbra lock
```

```
umbra: locked everything.tools.lock.json.gz (577 encoded bytes, 3 tools of 14 upstream)
umbra: locked specverb.lock (umbra v0.212.1)
```

Two locks, doing two different jobs.

* **The tool lock** is the granted surface, frozen. `lock` connects, runs
  `tools/list`, prunes to what the guardfile grants, and writes it. It gets
  committed, so everyone building this gets the same fourteen-to-three answer.
* **`specverb.lock`** pins the umbra module version the binary builds against.
  It is yours rather than this repository's, which is why it is a step you run
  rather than a file you are given.

`3 tools of 14 upstream` is the pruning said out loud. The eleven that did not
survive are not in the file at all, so the lock is not a record of what was
refused.

## Build and run

Nothing from here on reaches the network to mount.

```sh
umbra build --out ./mcpdemo
./mcpdemo ops everything --help
```

```
NAME:
   mcpdemo ops everything - guarded verbs over the MCP reference server

COMMANDS:
   echo                    call echo
   get-sum                 call get-sum
   get-structured-content  call get-structured-content
```

Three leaves from fourteen upstream tools.

```sh
./mcpdemo ops everything echo --message hello
./mcpdemo ops everything get-sum -a 20 -b 22
```

```
'Echo: hello'
The sum of 20 and 22 is 42.
```

`-a` and `-b` are typed because the server's own JSON Schema says they are
numbers. The guardfile never mentioned them, and `umbra skew` is what fails if
that schema moves.

## Two absences, and they match

```sh
./mcpdemo ops everything get-env
./mcpdemo ops everything get-tiny-image
```

```
mcpdemo: unknown verb "get-env" under "everything"; run --help for the verbs this binary grants
mcpdemo: unknown verb "get-tiny-image" under "everything"; run --help for the verbs this binary grants
```

Both exit **5**. `get-env` was closed deliberately, since a tool that reads the
process environment is the obvious thing to shut, and `get-tiny-image` was
never granted. **The binary cannot tell you which is which**, so a reader
of `--help` learns nothing about what exists upstream and an agent spends no
context on a verb it may not call.

This is where a generated binary differs from the in-process mount. It installs
its own handler for an unknown verb, so the answer is umbra's sentence and the
taxonomy's 5 rather than urfave/cli's fallback.
[primitives](primitives.md) has why 5 and not 2.

## The flags came from upstream

```sh
./mcpdemo ops everything get-structured-content --help
```

```
DESCRIPTION:
   Returns structured content along with an output schema for client data validation

OPTIONS:
   --dry-run          print the resolved tool call without firing it
   --query string     JMESPath projection applied to the result
   --output string    output format: yaml | yaml-stream | json | text | table
   --location string  Choose city (one of: New York, Chicago, Los Angeles)
   --help, -h         show help
```

`--location` carries its enum into the help text, because an enum is the one
constraint a caller cannot infer from the type. The `DESCRIPTION` is the
server's own, and `get-sum` shows the other case: its `describe` in the
guardfile overrides what upstream said.

`--dry-run` resolves the call and does not fire it, which is the cheapest way to
see what a guardfile actually built:

```sh
./mcpdemo ops everything get-sum -a 1 -b 2 --dry-run
```

```
arguments:
    a: 1
    b: 2
tool: get-sum
```

## Drift is the thing the lock buys

```sh
umbra skew
```

```
umbra: no skew; committed locks match upstream
```

`skew` prunes live upstream the same way `lock` did and diffs the two, exiting 3
on drift. It reports a tool that went away, one that appeared inside the granted
surface, and one whose input schema, output schema, `_meta`, annotations,
description or title moved.

**Nothing else locks MCP tool schemas.** Every other client reads the live
server, so it can print what a tool is today and cannot tell you it changed. An
upstream you do not control is exactly where that matters.

## It fails closed

Add `can call get-env` beside the `never` and lock it again.

```
umbra: umbra: parse mcp guardfile .umbra/everything.guardfile.kdl: mcpverb: tool "get-env" is both granted and denied; drop one (fail-closed)
```

Exit 1. A tool both granted and denied is a parse error rather than a
contradiction resolved on your behalf, because either resolution is a guess
about which sentence you meant.

The postcondition in the guardfile fails the same direction.
`fail-when "temperature == null"` rejects a call that succeeded and came back
without the field the caller needed, so a green exit means an answer arrived
rather than that a request did.

## Where to go next

* **[mcpapps](mcpapps.md)** - what a tool that ships a user interface may do
  once the host renders it. Read it last.
* **[the dialect reference](../docs/mcpverb.md)** - every grant and guard, and
  the credential rules a real upstream needs.
* **[the driver](../docs/umbra-cli.md)** - `lock`, `skew`, `build`, `run` and
  `install` against mixed transports.
