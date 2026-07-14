# opcore inline proxy grants

`proxy` is the inline MCP passthrough grant in `opcore.ParseInline`. It keeps
deny-by-absence on the served surface and lets the consumer resolve the
upstream tool schema from the exact mapping named in KDL.

## Grammar

```kdl
proxy browser_snapshot {
    upstream playwright browser_snapshot
    allow url matches "^https://grubhub\\.com/"
    deny text matches "forbidden"
    post-call content matches "grubhub\\.com"
}
```

## How each piece maps

* **tool name** - `proxy <tool>` names the local served tool.
* **upstream** - `upstream <server> <tool>` pins the exact upstream MCP tool.
* **allow / deny** - `allow|deny <field> matches <regex>...` guards request
  strings such as `url`, `target`, `element`, `text`, and `key`.
* **post-call** - `post-call <field> matches <regex>...` inspects returned
  `text`, `content`, `url`, or `state` for URL or forbidden-state checks.

See [opcore-inline.md](opcore-inline.md) for the full inline grammar.
