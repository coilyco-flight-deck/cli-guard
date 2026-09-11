# specverb request semantics

How the generic action behind every mounted leaf assembles, previews, and fires its request. Engine and policy layers are in [specverb.md](specverb.md).

## Inputs

- **Path params** become positional args, count-validated before any wire call. **Query params** become typed flags: scalars encode once, arrays as repeated keys in input order, unset values omitted.
- **Body fields** become typed flags, and an unset optional is omitted rather than sent as a zero value, and arrays repeat the flag. **`--body-file`** supplies the whole body instead. Required fields are enforced at assembly rather than in the CLI layer, so either source satisfies them.

A local input shadowing a reserved engine flag (`--dry-run`, `--query`, `--output`, `--body-file`), or a query/body collision on one leaf, refuses to build rather than shadowing silently.

## A mapped body carries a declared type

A `body` block written as `map "source.path" to="target"` projects a **string** unless the mapping says otherwise. `type=` declares what reaches the wire, and `items=` the element type of an array:

```kdl
body {
    map "search_text" to="query"
    map "contents"    to="contents"       type="object"
    map "limit"       to="numResults"     type="integer"
    map "domains"     to="includeDomains" type="array" items="string"
}
```

Supported types are string, integer, number, boolean, object, and array. An `items` of `any` takes each element as supplied, the union rule an empty swagger `items` schema implies. The declared type reaches the model-facing schema too, so the tool says what the wire will carry rather than always saying string.

**A caller supplying the wrong shape is refused before the request fires.** Mapped leaves once projected a string in every configuration, leaving an upstream that wants an object unreachable. The mode no longer restricts the type.

An unsupported type, an `items` outside an array, and an unknown property all fail at parse time. Reasoning: coilyco-flight-deck/umbra#312.

## The shell-metachar gate is location-aware

`verb.Wrap` → `policy.ValidateArg` refuses shell metacharacters, but only on the input that reaches the URL **unescaped**. That is the path, and only the path: `opcore.FillPath` substitutes each positional into the path template verbatim, so a metacharacter there composes into the request.

Query values are not gated. Every query string in umbra is built as `neturl.Values` and emitted through `Encode`, at all six assembly sites, and `Encode` percent-encodes the whole `ShellMeta` set along with `&` and `=`, the only two bytes that are structural in a query string. A brace in a query value therefore cannot alter the request, and the client fires HTTP directly rather than through a shell. Gating them bought nothing and made whole parameter classes unreachable: a JSON `filter`, a JSON `orderBy`, and a TQL expression all open with a brace, so an API that takes its sort or filter as a JSON value had no reachable sort or filter at all (umbra#6827).

Body and form fields and `--body-file` are exempt for the older reason: they are encoded into the body and never reach a shell or the URL. Gating them mangled legitimate free text.

`TestEveryShellMetaByteIsEncodedInAQueryValue` holds the encoding claim one byte at a time, so widening `ShellMeta` cannot quietly outrun the encoder.

## Opting a path param out

A path param that legitimately carries a metacharacter names itself in the wrap:

```kdl
wrap "teable" {
    spec "teable.swagger.json"
    can get record
    allow-metacharacters "recordId"
}
```

The gate then skips that param and no other. It is per-param rather than per-verb on purpose: a verb-wide switch would exempt every positional beside the one that needed it, which is the wrong shape for the reason the gate exists. There is no wrap-wide form, and there is no query form, since query needs none.

Until umbra#6827 the two refusal messages named `allow_metacharacters` as the remedy and no grammar implemented it, so the gate had no exit at all. The messages now name the node the grammar parses.

## A one-element array needs `encode="brackets"`

Query arrays reach the URL as the bare name repeated once per element, which is
correct at every length except one. `ids=a&ids=b` is unambiguously two values.
`ids=a` is indistinguishable from a scalar, so a server whose schema wants an
array refuses it and one that coerces accepts it. Both happen on the same API.

An array declares the shape it needs:

```kdl
array "selectedRecordIds" items="string" min-items=1 encode="brackets"
```

`brackets` emits `selectedRecordIds[]=rec1` at every length, so the arity is on
the wire rather than inferred from how many times the key appears. `repeat` is
the default and the historical behavior, and it stays the default because every
existing guardfile is written against it. `encode` on a non-array is refused.

Observed on Teable (umbra#7013): `selectedRecordIds` with one id returned
`expected array, received string`, `search` with one element returned `expected
tuple, received string` though its schema accepts a one-element tuple, and
`projection` with one element succeeded because that parameter coerces. Teable's
own client is axios with no `paramsSerializer`, so it emits the bracket form and
the backend parses it.

## Firing

Auth resolves the secret through the value-provider registry. `--dry-run` prints the resolved request with the secret redacted and fires nothing. Live responses render through the `--query`/`--output` rail, and an empty 2xx prints an `ok:` line. The client refuses redirects for mutating methods, so a renamed target cannot silently swallow a write.

A wrap may declare `header "<name>" "<value>"`, applied to every leaf, which is how an author states the contact address some APIs ask for. `Authorization` is refused, since `auth` owns it and a second path would be an unreviewed credential surface, and so is `Content-Type`, which the runtime sets from the body. A duplicate name, an empty value, and a wrong argument count fail closed. Absent a declared one, every request carries `opcore.DefaultUserAgent`.

## Non-JSON responses

An operation whose success response offers no JSON writes its body to stdout byte for byte. The spec-driven path infers this from the declared media type, and an inline grant says it outright with a bare `raw-response` node. A response listing JSON beside something else is negotiating content rather than declaring bytes, so it is parsed.

Both paths choose **before** firing rather than after reading (umbra#289): decoding first fails on a plaintext or ZIP body, leaving the raw branch unreachable. Only the decode is skipped, never a gate, and `--query` is refused rather than ignored.

Fixed non-Swagger leaves live in [fetch overlays](specverb-fetch.md).
