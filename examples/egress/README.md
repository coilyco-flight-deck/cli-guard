# egress example

The per-invocation CONNECT proxy with a pinned allowlist. Used internally by `passthrough.WithEgress` to gate package-manager network reach. Demonstrates allow / deny / observe modes plus the captured `EgressRow` audit shape.

```
$ go run ./examples/egress allowed
proxy listening on 127.0.0.1:xxxxx
response: 200 200 OK
egress rows:
  host=example.com:443 decision=allow up=... down=...

$ go run ./examples/egress denied
response: 0 (from proxy: 403)
egress rows:
  host=www.iana.org:443 decision=deny up=0 down=0
```
