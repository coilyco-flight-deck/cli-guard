# mount actions: shadowing a generated leaf

A complex action authored with **two** header arguments - `action <verb>
<resource>` instead of `action <name>` - mounts at that leaf path, taking the
place of the generated leaf there. This is how a default verb grows behaviour:
the operator keeps invoking `forgejo issue view`, but it now resolves to a
composite that fetches the issue **and** its comment thread. See
[specverb-actions.md](specverb-actions.md) for the base action grammar.

```kdl
wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    base-url "forgejo.coilysiren.me/api/v1"
    auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }

    can view issue                                   // the leaf the action calls and shadows
    can list issue-comment { op "issueGetComments" } // the comments sub-collection

    action view issue {                              // two args => mount at `issue view`
        describe "View an issue with its full comment thread."
        input source { positional; required; help "owner/name" }
        input index  { positional; required; help "issue number" }
        call view issue {
            args { owner-repo $source; index $index }
            as issue
        }
        call list issue-comment {
            args { owner-repo $source; index $index }
            as comments
        }
    }
}
```

Three things follow from the two-arg form:

- **It shadows.** The generated `issue view` leaf is dropped from the CLI and
  the describe surface; the action mounts in its place. The `can view issue`
  grant still resolves, so the action's own `call view issue` reaches the op -
  the shadow replaces the CLI leaf, never the grant.
- **It combines.** A mount call-action renders **every** `as` binding together
  as one object (`{issue: ..., comments: [...]}`), not just the final call's
  response the way a named `call` action does. `--query` can project that
  combined response and `--output` formats it like any other response (YAML
  sorts the keys).
- **It keeps the leaf's audit identity.** The envelope row is named for the
  shadowed path (`<group>.<resource>.<verb>`, e.g. `ward.ops.forgejo.issue.view`)
  so audit and metrics for that verb stay continuous. Each inner call still
  writes its own leaf row.

A mount action may also be a `poll` (it renders the final response at the leaf
path). The grammar is otherwise identical; only the header arity differs.
