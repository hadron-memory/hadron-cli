# Markdown body framing (CLI #945 / server #2585)

The frontmatter Markdown codec now removes exactly the framing that the existing
writers add: one leading LF after the closing fence line, and one final LF if
present. Every other byte of a nonempty body is preserved, including indentation,
tabs and authored boundary newlines. Export bytes are unchanged.

After removing the frame, empty or ASCII-whitespace-only content (space, tab, LF,
CR, vertical tab, form feed) means no body. The CLI represents this as an empty
Document content string, so node import omits `content` on both create and update;
this never clears a stored body. Unicode-only whitespace (NBSP, BOM, NEL, line or
paragraph separator) remains content. JSON import/export is unchanged.

CRLF is normalized to LF across the whole file before parsing, as before. A
content-final CR can join the exporter's final LF and is consequently lost. A
lone CR elsewhere remains content. A handwritten leading blank line is ambiguous
and is consumed as framing; extra trailing blank lines beyond one are content.
Handwritten files without either framing newline remain accepted.

Markdown input `contentHash` is discarded. Export always recomputes the hash from
the unframed Document content; import never sends the file's hash to the server.
No approval hash formula, server mutation or schema changes are made here.

## Shared fixture and lockstep

`internal/nodedoc/testdata/markdownBodyFraming.fixture.json` is a byte-identical
copy of the server-owned `markdown-body-framing-v1` fixture from
`src/integrations/localFs/markdownBodyFraming.fixture.json` in hadron-server#2585,
revision `0aab43f5a45e1fb5b79ed6c3b380d8c9925ec85a`. Both implementations consume
all 27 cases. Keep the copies synchronized when the server owner changes them.
Neither companion merges before both have approved frozen heads. Use Refs #945;
the issue stays open until both implementations land.

Codec tests parse every fixture, check writer framing for writer cases, pin
preserved hashes, discard stale input hashes and keep JSON whitespace unchanged.
Command tests capture actual create/update mutation inputs for every fixture,
including absent-content omission. Existing Markdown export goldens remain the
byte-compatibility anchor.

## Release note

Markdown imports now preserve significant body whitespace, including indented
code and authored boundary newlines. Only one framing newline at each end is
removed; empty or ASCII-whitespace-only bodies remain absent. Extra trailing
blank lines in handwritten files now become content. CRLF normalization and the
content-final CR ambiguity remain limitations; JSON is unchanged.

Server companion: on the first git resync, a file with whitespace missing from
its stored body can produce one revision and supersede approval, because the
repository is the source of truth. Files rendered from current stored content
stay unchanged. This CLI change does not itself perform a resync or activation.
Legacy server node.yaml/topic.md loaders remain outside this cut (#2584).
