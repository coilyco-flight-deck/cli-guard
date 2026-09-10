# mcpapps

MCP Apps lets a tool ship a user interface. The server returns a widget, the
host renders it, and from then on the widget talks to the host directly:
listing tools, calling them, reading resources, opening links, saving files.

That is a second caller with its own reach, arriving from a server rather than
from your code. This guide is about the `widget` block that decides what it may
do, and it is the last guide for a reason. Read
[mcpverb](mcpverb.md) first, because a widget's grants are written in the same
language as a tool's.

Read this one rather than run it. The sequence below was replayed against a live
session and captured whole.

## The block

A `widget` block hangs off the tool whose view it governs.

```kdl
tool get_system_info {
    widget {
        can call poll_system_stats
        can read "^ui://"
        can open "^https://"
        can save "^ui://"
    }
}
```

Four grants, and each one answers a question the host would otherwise answer by
trusting the widget:

* **`can call`** - which tools this view may invoke, out of everything the
  session holds.
* **`can read`** - which resource URIs it may fetch.
* **`can open`** - which links the host will open on its behalf.
* **`can save`** - which URIs it may hand to the host as a download.

Anything not granted is refused, and the refusal names the clause that would
have permitted it.

## The whole sequence

This is one session end to end. Requests from the widget are `<-` and the host's
answers are `->`. Payloads are truncated where they run long.

```
view for get_system_info lives at ui://monitor/view.html

<- ui/initialize
-> result     {"id":0,"jsonrpc":"2.0","result":{"hostCapabilities":{"downloadFile":{},"openLinks":{},"serverResources":{"listChanged":false},"serverTools":{"listChanged":false}},"hostContext":{"displayMode":"inline","theme":"dark"},"h...
<- ui/notifications/initialized
-> ui/notifications/tool-result {"jsonrpc":"2.0","method":"ui/notifications/tool-result","params":{"content":[{"text":"{\"host\":\"example.local\",\"platform\":\"darwin arm64\"}","type":"text"}]}}
<- ui/notifications/size-changed
   (no reply: a notification needs none)
<- tools/list
-> result     {"id":1,"jsonrpc":"2.0","result":{"tools":[{"inputSchema":{"properties":{"scope":{"type":"string"}},"type":"object"},"name":"poll_system_stats"}]}}
<- tools/call poll_system_stats
-> result     {"id":2,"jsonrpc":"2.0","result":{"content":[{"text":"{\"memory\":\"5.4 GB / 18.0 GB\",\"scope\":\"cpu\",\"uptime\":\"13h 38m\"}","type":"text"}],"isError":false,...
<- tools/call poll_system_stats (with a progress token)
-> notifications/progress {"jsonrpc":"2.0","method":"notifications/progress","params":{"message":"sampling","progress":1,"progressToken":"view-1","total":2}}
-> result     {"id":7,"jsonrpc":"2.0","result":{"content":[{"text":"{\"memory\":\"5.4 GB / 18.0 GB\",\"scope\":\"cpu\",\"uptime\":\"13h 38m\"}","type":"text"}],"isError":false,...
<- tools/call poll_system_stats (guarded argument)
-> refused    {"error":{"code":-32001,"message":"policy_denied: argument scope=\"secret-partition\" is outside the allowed scope (deny scope matches [^secret])",...
<- tools/call wipe_disk (ungranted to the view)
-> refused    {"error":{"code":-32001,"message":"policy_denied: tool \"wipe_disk\" is not granted to the view of get_system_info; add `can call wipe_disk` to its `widget` block",...
<- resources/read file:///etc/passwd
-> refused    {"error":{"code":-32001,"message":"policy_denied: \"file:///etc/passwd\" is not readable by the view of get_system_info; add `can read` to its `widget` block (it permits \"^ui://\")",...
<- resources/list
-> result     {"id":6,"jsonrpc":"2.0","result":{"resources":[{"uri":"ui://monitor/view.html","name":"ui://monitor/view.html","mimeType":"text/plain"}]}}
<- ui/open-link https://example.com
   host would open https://example.com
-> result     {"id":8,"jsonrpc":"2.0","result":{"isError":false}}
<- ui/open-link http://example.com (not https)
-> refused    {"error":{"code":-32001,"message":"policy_denied: \"http://example.com\" is not openable by the view of get_system_info; add `can open` to its `widget` block (it permits \"^https://\")",...
<- ui/download-file ui://report.csv
   host would save ui://report.csv (text/csv)
-> result     {"id":10,"jsonrpc":"2.0","result":{"isError":false}}
<- ui/download-file file:///etc/passwd
-> refused    {"error":{"code":-32001,"message":"policy_denied: \"file:///etc/passwd\" is not savable by the view of get_system_info; add `can save` to its `widget` block (it permits \"^ui://\")",...
<- ui/request-display-mode (undeclared capability)
-> refused    {"error":{"code":-32601,"message":"not implemented: ui/request-display-mode"},"id":12,"jsonrpc":"2.0"}
```

## What to look at

**`tools/list` answers with one tool.** The session has three. The view asked
what it could call and was told about `poll_system_stats` and nothing else, so
the widget's own picture of the world is already narrowed. It never learns
`wipe_disk` exists. This is the same deny-is-absence property you met on the
command line, moved into a protocol reply.

**Then `wipe_disk` is called anyway, and refused by name.** A widget can attempt
anything it likes regardless of what the listing said, which is why the listing
is a convenience and the guard is the control. Never treat a narrowed list as
enforcement.

**Every refusal names the clause that would have permitted it.** Not
`permission denied`, but `add can call wipe_disk to its widget block`, and for
the URI cases the pattern the grant currently holds, such as `it permits
"^ui://"`. That is aimed at whoever is writing the guardfile rather than at the
widget, and it is the difference between a policy you can adopt and one you
argue with.

**A granted tool is still guarded on its arguments.** `poll_system_stats` is
called three times. It works, it works again with a progress token, and then it
is refused for `scope="secret-partition"`. `can call` grants the tool, and the
tool's own argument guards still apply, so a view cannot use a permitted tool to
reach something the tool itself would not.

**Four kinds of reach, four separate grants.** Calling, reading, opening and
saving are independent. A view that may read `ui://` resources still cannot save
`file:///etc/passwd`, because `can save` is its own decision. Bundling these
would mean granting a chart the ability to write to disk in order to let it
read its own template.

There is a fifth verb, `can connect`, and it does not appear in this log because
it is not enforced here. It is the subject of the last section.

**The last one is a different kind of no.** `ui/request-display-mode` is refused
`-32601 not implemented` rather than `policy_denied`. The host never declared
that capability in `ui/initialize`, so there is nothing to permit or refuse. A
policy refusal and an absent capability are distinct answers, and a widget
author needs to tell them apart: one is a guardfile edit, the other is a host
that cannot do it at all.

## Enforced, and merely computed

There is a second kind of grant in the `widget` block, and the difference
between the two is the thing to understand before you rely on either.

**The four above are enforced.** A `tools/call`, a `resources/read`, an open and
a download are answered by the host under the `widget` block. Refused means it
did not happen.

**`can connect` is computed and delegated.** umbra derives a Content Security
Policy from what the view declared and hands it over. The consumer's presenter
has to apply it to the frame, and the browser is what enforces it. umbra cannot
make that happen and does not check that it did.

So the line worth holding in your head is that **umbra enforces what the widget
asks the host to do, and only describes what the widget may reach on its own.**
If a consumer drops the policy on the floor, every grant in the `widget` block
still holds exactly as shown above, and the page can talk to anything it likes.
Nothing in the frame log would look different.

Two limits on the computed half, worth knowing before you write one:

* **Only `connect-src` has a guardfile verb today.** The script, style, image,
  font and media families have none, so they fall back to the specification's
  default rather than to anything an author declared. A `widget` block is not
  yet a full statement of what a view may load.
* **`Report` names a violation collector and grants nothing.** It is umbra's
  addition rather than the specification's, and it tells you a policy was
  breached rather than preventing the breach.

Beyond that, the boundary is the same one the [replacement
guide](replacement.md) draws. This is a control over what a caller may ask for.
It is not a claim about everything that could go wrong beneath it, and what the
server does before it hands you a widget is its own question.

## Where to go next

You have read the whole ladder. [The driver](../docs/umbra-cli.md) covers
authoring guardfiles for real, and [architecture](../docs/architecture.md)
covers the two surfaces underneath everything here.
