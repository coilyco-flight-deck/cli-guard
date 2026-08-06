# the top-level `description` node

Every `.kdl` spec may carry a first-class top-level `description "..."` node: the
durable "what this spec is and does" prose, sibling of the root block, present on
all three dialects (fleetconfig's `agents`, the http/guardfile `wrap`, and the
exec-dialect `wrap`). It is **queryable contract data, not a comment header** -
the sanctioned home for standing context the `code-comments` header exemption
used to carry.

```kdl
description "Forgejo ops surface for ward-kdl: scoped read/write over the coily* orgs."
wrap ward-kdl ops forgejo {
    // ... policy ...
}
```

## Shape

A single string argument. KDL's escaped and multi-line string literals carry
multi-paragraph prose when needed; an empty `description ""` fails closed, so the
node is never a silent no-op.

## What belongs here

The durable "what/why" a reader needs to understand the surface. Changelog and
provenance archaeology (which test pins the file, which `make` target syncs it,
issue history) is **not** runtime description. Keep that detail in a maintained
`docs/*.md` walkthrough rather than the runtime command surface.

## How it surfaces

- **The two guardfile dialects** (http/guardfile, exec) flow the prose into the
  describe surface (`<cli> ... describe`), rendered as a paragraph under the
  H1.
- **fleetconfig** parses it into `Fleet.Description` for the consumer (e.g.
  `ward-kdl`) to surface.

Origin: the KDL description node.

## See also

- [specgen.md](specgen.md) - the no-code driver over these specs.
- [fleetconfig.md](fleetconfig.md) - the fleet-config dialect.
