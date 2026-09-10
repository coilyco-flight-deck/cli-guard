Held drafts, not published documentation.

Four finished guides by Gem (advocate), verbatim, carrying a .txt suffix
only because they are not yet where they will live. They belong at
guides/<name>.md and cannot go there yet:

- two more guides are unwritten (mcpverb, mcpverb-cli)
- they cross-link to each other and to files that do not exist yet
- quickstart is 273 lines against the 240-line guide cap, and trimming
  it is an editorial call on the advocate seat's prose rather than the
  platform seat's
- guides/ needs the aos-precommit pin bumped to a release whose
  documentation-placement rule knows the directory, which is a change
  worth making deliberately rather than inside a rescue

They are committed here because the session that wrote them ended and a
scratchpad is not a store. Rename each to guides/<name>.md when the set
is complete. See teable:coilyco-flight-deck/umbra#7333.

from-deleted-examples.txt is captured tool output, also not documentation.
