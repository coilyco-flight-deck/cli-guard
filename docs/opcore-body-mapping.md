# Inline request body mapping

An inline operation can project required nested string inputs into a fresh
top-level JSON request body:

```kdl
can create message {
    path "/sendMessage"
    body {
        map "commonAnnotations.summary" to="text"
        map "commonLabels.alertname" to="alert_name"
    }
}
```

For this input:

```json
{
  "commonAnnotations": {"summary": "API errors are high", "secret": "ignored"},
  "commonLabels": {"alertname": "api-errors"},
  "ignored": "not forwarded"
}
```

the operation sends only:

```json
{"text":"API errors are high","alert_name":"api-errors"}
```

Each source path traverses objects only. Every source is required and must
resolve to a string. Destinations are simple top-level keys.

This feature is deliberately smaller than a template language. It does not
concatenate strings, loop, evaluate expressions, or transform responses. A
mapped body cannot also declare ordinary body fields or a fixed `set` body.
Duplicate sources, duplicate destinations, parent/child source collisions,
malformed paths, missing values, and non-string values fail closed before an
upstream request fires.
