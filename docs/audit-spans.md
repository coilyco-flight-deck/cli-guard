# audit records as spans

umbra's append-only audit log projects onto tracing spans, so a refusal is a
telemetry signal rather than a log artifact and an operator can see prevented
actions as a **population** rather than one at a time (umbra#6817).

## The claim this is built against, and the one it is not

The wide claim is occupied one layer up. Harness ships Agent Trace, and
Coralogix and Oracle both describe governance signals recording which policies
blocked an action. They instrument the model and policy layer.

What survives is narrower, and is the only claim this supports: **umbra
instruments the process boundary and already publishes an exit-code taxonomy
separating refusal from failure**, which none of those does. Do not restate the
wider claim.

## Two layers, and umbra ships both

`Record.SpanOf()` returns an `audit.Span`: a name, a start time, a duration, an
error flag, and flat attributes, with no OTel type in the signature. That
projection is dependency-free and is what `Writer.Sinks` feeds.

`pkg/audit/otelsink` binds it to OpenTelemetry:

```go
tp, err := otelsink.NewProvider(ctx, otelsink.ProviderConfig{ServiceName: "aosguard"})
defer tp.Shutdown(ctx)

w := audit.NewWriter(path)
w.Sinks = []audit.Sink{otelsink.New(tp.Tracer(otelsink.ScopeName))}
```

`otelsink.New` takes any `trace.Tracer`, so a consumer that already has a
provider passes it and ignores `NewProvider` entirely. A nil tracer makes
`Emit` a no-op: a consumer that has not wired a provider is not a crash.

**The dependency cost is real and worth knowing.** The OTLP exporter pulls
gRPC and protobuf transitively, and umbra is the base of the umbra/ward stack,
so this reaches every generated binary. A consumer wanting only the projection
can still use `audit.SinkFunc` and import none of it, but the module requires
are in umbra's `go.mod` either way.

Sinks run **after** the durable JSONL write and cannot return an error.
Telemetry never fails an audited invocation, and the log never depends on a
collector being reachable.

An audit record describes completed work, so the span opens at the record's own
timestamp and closes at start+duration rather than at export time.

## Refusal is not an error

`OutcomeFor` classifies the exit code into four outcomes, and this is the whole
point of the projection rather than a detail of it:

- `Success` - outcome `ok`, no span error
- `PolicyDenied` - outcome `refused`, **no** span error
- `Internal` - outcome `internal`, span error
- everything else - outcome `failed`, span error

A refusal is a **successful boundary**. Marking its span an error would bury it
in the same bucket as a broken upstream, which is exactly the confusion the
exit-code taxonomy exists to prevent. `umbra.refused` is a boolean attribute so
a query selects the population directly, without parsing an error string.

On the emitted span this is the OTel status: a refusal is `codes.Ok`, and a
failure is `codes.Error` described by its taxonomy token. So a dashboard
filtering on span error status will show **zero** refusals and look healthy
while every refusal is happening. Filter on `umbra.refused` instead.

## Attributes

`umbra.verb`, `umbra.decision`, `umbra.outcome`, `umbra.exit_code`,
`umbra.kind`, `umbra.refused`, and, when set, `umbra.session_id`,
`umbra.repo_root`, `umbra.cache`, `umbra.policy_skipped`,
`umbra.egress_host_count`. Unset fields are omitted rather than emitted empty,
so a backend does not index a column of blanks.

The keys are namespaced so a collector can select umbra's rows without matching
on a verb name, and they are **stable**: a consumer's dashboard queries them.

The span is built from the record **after** redaction, so a sink can never carry
a value the JSONL would not.

## What is not here

The operator dashboard umbra#6817 also asks for. It renders in a hosted
observability surface against a consumer's exporter, so it is neither umbra's
code nor umbra's call.
