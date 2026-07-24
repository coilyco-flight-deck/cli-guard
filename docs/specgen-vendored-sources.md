# specgen vendored sources

A spec member normally derives a live Swagger URL from `base-url`. A consumer
may instead commit the API contract beside its KDL member and name it with the
`spec` node. Specgen reads that local source during `lock` without reaching the
live endpoint.

## Encodings

Plain JSON and YAML remain supported. A source ending in `.gz` is decoded with
a 128 MiB output limit before parsing and pruning. This covers `.json.gz`,
`.yaml.gz`, and `.yml.gz` without changing the logical API contract.

Invalid or oversized gzip fails the lock operation. Specgen never treats a
present but unreadable vendored source as permission to fetch the network
copy. A missing local source may still fall back to the derived URL, preserving
the existing online lock workflow.

## Artifact names

The source encoding suffix does not become part of the generated lock name.
These inputs both produce `forgejo.swagger.lock.json.gz`:

```kdl
spec forgejo.swagger.v1.json
spec forgejo.swagger.v1.json.gz
```

The vendored source is an operator-owned input. The generated lock is a
machine-owned, pruned contract. Both may use gzip, but they retain separate
names and ownership.

## See also

* [Specgen lifecycle](specgen.md)
* [Materialization](specgen-materialization.md)
