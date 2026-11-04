# Reading an encrypted memory: `--unlock` (#1234)

Design as built. Contract: the #1234 issue body (Patrick, settled per Xanthos's
GAPS 6622845084), spec fit Victor 6511624034 (cor:sec:040:04), ruling **0129**
(Holger, "(a)": per invocation only; never argv, env or cache). Gate: CHECKED
T2 + §10 at every head (Xanthos). No server change; no schema refresh. The
server contract read is hadron-server `src/lib/memoryUnlock.ts`
(`stageRequestMemoryKeys`) and `src/server.ts` (its call in the Apollo context
function) at origin/main 3aa75ff8.

## Surface

Three GLOBAL flags on the root, so any command (and `hadron api`) can read an
encrypted memory the caller owns:

- `--unlock <memory-ref>`, repeatable, at most 8 (the server's
  `MAX_HEADER_ENTRIES`). Refs are canonicalized with `CanonicalMemoryRef`;
  an id passes through. A duplicate after canonicalization is refused.
- `--unlock-passphrase-file <path>`, repeatable, paired with `--unlock` **by
  order**; the counts must be equal. The issue leaves multi-memory file
  pairing open; order pairing keeps one flag and no `<ref>=<path>` parser, and
  a count mismatch is a refusal, not a guess.
- `--unlock-passphrase-stdin`: all of stdin is the passphrase for exactly one
  `--unlock`.
- Neither source flag, with stdin a terminal: a no-echo prompt per memory
  (`term.ReadPassword`, label `Passphrase for <ref>` on stderr).

There is no flag that takes a passphrase value and no environment variable
(ruling 0129); a pin asserts both. v1 is passphrase-only: the header's
`dataKey` form is not offered (Victor delta 3), and help says so.

Passphrases are trimmed exactly as `memory encrypt --passphrase-file` trims
them (`TrimRight "\r\n"`), so the file that encrypted a memory unlocks it.

## When it is read

`cmdutil.ResolveUnlock` runs first in the root's `PersistentPreRunE`, before the
ended-binding check and before any command can build a client. Every refusal
is exit 2 with nothing sent: over 8, a duplicate, an empty ref or passphrase, a
source flag without `--unlock`, stdin together with a file, stdin with two or
more memories, a file-count mismatch, no source on a non-terminal, or a file
that fails the checks below. `api.WithMemoryKeys` also enforces the 8-entry
bound and the server's 16 KiB header limit locally.

**File checks (the ssh key rule).** `Stat` first: a non-regular file (FIFO,
device, directory) is refused before it is opened, since opening a FIFO for
reading blocks. The Stat alone cannot promise that, because a FIFO can replace
the file between the Stat and the open (PR #1616 BLIND P2-2). So the open
itself is non-blocking (`O_NONBLOCK`; a regular file ignores the flag). Then
`Stat` the opened file: it must be the same
regular file (`os.SameFile`) with no group/other permission bits
(`perm & 0o077 == 0`). Errors name the file by position (#1, #2…), never the
path (#1490: a passphrase pasted as a path must not be echoed). On Windows the
POSIX bits are meaningless, so the file source is refused there, failing
closed, and the prompt or stdin is used instead.

## On the wire

The keys go on the invocation's command context (`cmd.SetContext`), never on
the client or the Factory, so they ride the requests THIS invocation makes and
die with it. `bearerDoer.Do` sets `X-Hadron-Memory-Keys` to the JSON array
`[{"memory","passphrase"}]` for a context that carries keys, as with the
per-call operation key (#846). `RawGraphQL` (`hadron api`) does the same.

A key-bearing request reuses the operation key's guards:
- It is refused on cleartext http (`checkInitialSessionRequest`; loopback and
  `HADRON_ALLOW_HTTP` excepted).
- It refuses a cross-host or insecure redirect (`checkSessionRedirect`, under
  both redirect policies). A mutation still refuses every redirect (#1571).

## Errors

The server stages the header in the Apollo context function, before any
resolver runs. A failure is a `GraphQLError` with `extensions.code`, no `path`,
answered as HTTP 500 with an errors envelope. `nullSafeGraphQLClient.MakeRequest`
turns a path-less unlock code on a keyed request into a typed error:

- the message is fixed per code ("--unlock refused (WRONG_KEY): …"), never the
  server's text, so it cannot carry a secret;
- `serverCode` goes to the `--json` envelope;
- exit codes follow the existing contract: `WRONG_KEY`, `BAD_UNLOCK_INPUT`
  and `NOT_ENCRYPTED` exit 2, `NOT_FOUND` exits 4, `UNAUTHENTICATED` exits 3.
  `NOT_ENCRYPTED` also joins the shared code table, for the raw path.

The same code WITH a path is a resolver's opinion and keeps its ordinary
mapping. Nothing is retried. The server does not say which entry failed, so
neither does the CLI.

## Redaction

The CLI has no verbose, debug or request-dump output, so the header's only exit
paths are error renderings. For a keyed request the doer scrubs the following
secrets:
- the encoded header;
- each passphrase;
- each passphrase's JSON-escaped spelling (the header is JSON, so an echo of it
  carries that spelling);
- the URL-escaped spellings of all of these.

It scrubs only what can be rendered as an error:
- the whole of a non-200;
- the `errors` list of a 200 envelope;
- the whole of a 200 that is not a JSON object.

Successful `data` is NEVER rewritten. Unlike the 32-byte operation key, a
passphrase can be short or a common word, and the plaintext the user asked for
must come back intact.

A JSON body is scrubbed by structure, never as bytes (PR #1616 BLIND P2-1).
The refusal is classified from the scrubbed body, so a passphrase such as
`errors` or `KEY` must not rewrite the envelope's keys or the `WRONG_KEY` code.
String values are scrubbed, and so are object keys other than the GraphQL
envelope's own (`data`, `errors`, `message`, `locations`, `path`, `extensions`,
`code`, …). An identifier-shaped `extensions.code` is kept as protocol. A body
that is not JSON (an HTML error page) has no structure to keep and is scrubbed
as bytes.

## Cost

Each request carries the header, so the server derives the key once per entry
per request: scrypt N=32768, r=8, p=1 (`hadron-server:findings:encrypted-memory-keys-have-no-post-response-consumer`
measured about 58 ms each). A paging command with N requests and k memories
pays N×k derivations. This is the per-request model cor:sec:040:04 prescribes.
A cache would change it and is out of scope (ruling 0129: its own issue and §10).

## Not in this change

- **Docs companion:** `hadron-cli.md` in hadron-docs gains `--unlock`. That is
  another repo's change, so this PR leaves #1234 open until it lands.
- **Not covered:** a raw `dataKey` source, a passphrase cache, and any server,
  MCP or portal change.
- **Not verified here:** search matching inside encrypted content. The help
  keeps the existing statement that words inside it are not found.
