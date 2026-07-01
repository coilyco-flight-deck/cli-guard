# specverb value chains: a fallback list

A `value` names where a config value (an auth secret, a `base-url` host) is read at request time. See [specverb-policy.md](specverb-policy.md) for the single-source form and the provider registry. This page covers the ordered fallback list.

## The children-block form

KDL has no arrays, so an ordered fallback list sits in a children block, one source per line. Resolution walks the chain and takes the first source that yields a non-empty value with no error - prefer a fast local `env`, fall back to a durable store:

```kdl
auth header-token {
    header Authorization
    value {
        env FORGEJO_API_TOKEN                    // fast local, checked first
        ssm "/forgejo/coilyco-ops/api-token"     // durable backup
    }
}
```

Every field that takes a `value` takes a chain: the three auth schemes (`header-token`, `bearer`, each `query-param` secret) and `base-url`.

## Backward compatibility

The inline `value <provider> "<address>"` is a one-element chain, so every existing Guardfile is unchanged. The two forms are mutually exclusive on one node.

## Fail-closed at parse time

The grammar rejects these shapes when the Guardfile is parsed, never at request time:

- an empty block (`value { }`) - list at least one source.
- a source missing its address (`value { env }`) - each source is `<provider> "<address>"`.
- a mixed inline-and-block form (`value ssm "/a" { env FOO }`) - pick one.
- a source carrying its own children or properties.

## Resolution and safety

`valuesource.ResolveFirst` walks the sources in order. A source is skipped when its provider errors OR resolves an empty value (success needs both: no error AND a non-empty value). The first success wins and later sources are never consulted.

When every source fails, resolution returns a combined error naming each provider and address it tried, but never a resolved value - a provider that hands back a value alongside an error still leaks nothing. A failed chain fails closed before the request fires.

A `--dry-run` stays offline and shows the chain symbolically as `<provider> <address>` sources joined by ` | `; describe names them the same way. Neither resolves a source, so neither shows a value.
