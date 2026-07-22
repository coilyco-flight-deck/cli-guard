# Agent claim contract

The `pkg/agentclaim` package defines the policy-free identity facts that a
context producer and an authority launcher can share without depending on each
other.

## Boundary

The contract separates two role domains:

* `context` - organizational purpose and context selection.
* `authority` - independently resolved execution authority.

A producer may emit either role domain or both. Matching names do not connect
the domains. An authority consumer must never grant access because a context
producer emitted the same role name.

The package treats role names as opaque strings and embeds no organization
roster. It also keeps agent, model, model class, harness, and reasoning effort
as independent optional facts. The package derives no fact from another fact.

The package excludes personality policy, guardfiles, permissions, credentials,
launch arguments, task acceptance, and consumer defaults. Each consumer owns
those decisions above this structural contract.

## KDL form

One document contains exactly one versioned `agent-claim` node:

```kdl
agent-claim schema-version=1 {
    agent "example-agent"
    role "builder" domain="context"
    role "reviewer" domain="authority"
    model "example-model"
    model-class "frontier"
    harness "example-harness"
    reasoning-effort "high"
}
```

The parser requires at least one role. The parser accepts at most one role in
each domain and at most one of every scalar fact. Unknown vocabulary, malformed
values, and unsupported versions fail closed.

`ParseKDL` accepts a complete one-node document. A consumer that embeds the
same node inside a larger KDL contract passes the parsed child to `ParseNode`.
Both entry points apply the same structural validation.

The parser allows partial subjects because different producers know different
facts. A consumer applies its own completeness rules after structural
validation. For example, a context bundle producer may require a context role
and harness, while an authority launcher may require an authority role and
independently authenticated policy.

## Machine-readable projection

The exported Go types carry stable JSON field names so another manifest can
embed a claim without defining a second identity model. Role order follows the
source order. A producer that needs deterministic bytes preserves that order
and uses the standard JSON encoding of the exported types.

## See also

* [architecture.md](architecture.md) - the downward-only package boundary.
* [fleetconfig.md](fleetconfig.md) - the separate fleet launch and guardfile
  schema.
* [FEATURES.md](FEATURES.md) - shipped package inventory.
