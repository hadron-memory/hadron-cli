# The edit round trip: `--body-only` reads and body write guards (cli#873)

Design as built. Issue: hadron-cli#873 (CHECKED T1, Xylon), with Vera's scope
addition (team chat #10943, issuecomment-6159089903).

## The problem

Two reads were documented or used as the source for an edit, and both corrupt
a body written back from them:

- **Text-mode `node get --raw`** prints a metadata header (name, `loc`, `urn`,
  `URL`, type, edges, `content: stored body …`) above the body. `node get
  --help` said "to edit a node's content, read it with --raw", so piping that
  output into `node update --content-file` stored the header *inside* the
  body. Three runnable task nodes were damaged this way in the field.
- **`spec get --body-only`** promised "the raw markdown body" but read through
  the rendered `GetNode`, so Mustache was compiled away. A spec whose first
  line was a `{{=<% %>=}}` delimiter change lost that line through an edit
  made from it, and its literal `{{name}}` text then rendered as empty.

Until now, the only safe source for an edit was `node get --raw --json` →
`.content`.

## What changed

| Surface | Change |
| --- | --- |
| `node get --body-only` (alias `--content-only`) | New. One ref only (several or `--prefix`: exit 2, no request). Reads `GetNodeRaw` and prints the stored body **byte for byte**, with no newline added unless stdout is a terminal. `--json`: `{id, urn, loc, content, rendered: false}`. It implies `--raw`. |
| `spec get --body-only` | Now reads raw (`fetchRawSpec` / `GetSpecNodeRaw`), and no longer appends a newline when piped. The `--json` shape `{citation, content}` is unchanged. |
| `node update` | New body (`--content`, `--content-file`): a header-prefixed body is **refused** (exit 2, before any request) unless `--force`. A body that drops a `{{…}}` tag the stored body has is written, with `warning:` on stderr. `--content-file -` reads piped stdin, the same as `--content -` (the acceptance's round trip spells it that way), and is refused from a terminal. |
| `node update --dry-run` | New. It reads the node raw, runs every client-side check a write runs (header guard, the role and governed-kind refusals, input parsing), and writes nothing; the server's own validation runs only on the write. Output is the changed fields plus a unified diff of stored → proposed body. `--json`: `{dryRun, id, urn, loc, fields[], content: {before, after, diff} \| null, warnings[]}`. `--dry-run --data-merge` is refused: the merge is a separate mutation the preview doesn't cover. |
| `spec edit` | Same two guards on a changed body, dry run included, with `--force`. Warnings go to stderr and into the result's new `warnings` list (always present, `[]` when there are none). |
| help text, agentic-usage | The edit read is `--body-only` everywhere; `--raw` text output is no longer recommended for edits. |

The shared logic lives in `internal/cmdutil/bodyguard.go`
(`GetHeaderPrefix`, `RefuseGetHeaderPrefix`, `DroppedTemplateTags`,
`TemplateDropWarning`), so both commands refuse and warn with the same words.
`withFlagAliases` moved from the spec package to `cmdutil.WithFlagAliases` so
`node get` can share the `--content-only` alias.

## Decisions

- **`--body-only` implies raw on both commands.** A body-only read exists to
  feed an edit, and a rendered body-only read is exactly the lossy path being
  removed. `spec get --body-only` already documented itself as raw. Nothing
  offers a rendered body-only read.
- **Refuse the header, only warn on dropped tags.** A body never legitimately
  starts with the get header, so `--force` is the explicit escape. Removing a
  placeholder can be the point of an edit, so a dropped tag warns but does not
  block. That matches the acceptance ("refuse a get-header prefix", "a
  warning when a proposed body drops …").
- **Header detection is narrow.** node get: a non-indented title line,
  `  loc: <value>`, then one of the lines the renderer prints next (`  urn: `,
  `  URL: `, `  type: `), so a YAML-ish `deploy:` / `  loc: us-east` body is
  not caught. spec get: a title line, then a whitespace-free reference (the
  `<org>::<memory>::<loc>` it prints today, or an `hrn:node:` URN), possibly
  after the `PLACEHOLDER` line. A first line starting with `#` never matches
  the spec form, because spec get prints the plain name. The command tests feed
  each renderer's real text output to the guard, so the detector can't drift
  from the printer unnoticed. `--force` covers any false positive that remains.
- **Tag comparison is on distinct tags in the default delimiters.** A tag the
  new body still contains somewhere is not reported, even if one occurrence
  was removed. Tags written in a custom delimiter (`<%who%>` after
  `{{=<% %>=}}`) are not tracked; losing the delimiter line itself is what
  gets flagged.
- **`node update` reads the node raw when the body changes or on a dry run**
  (`GetNodeRaw` replaces the kind read's `GetNode`, with the same fields). The
  extra round trip already existed; only which read it uses changed. On a
  real write the read stays best-effort, so a failed read skips the warning
  and the server still gives the authoritative refusal. A dry run fails on a
  failed read.

## Not done here (by design)

- **Text-mode `node get --raw` still prints the header.** Making it
  body-only is an output-contract change that scripts may rely on. The issue
  orders it last, as a separate decision. With `--body-only` in place and the
  write guard refusing the header, the corruption path is closed without it.
- No server or spec change: these are client-side read and write-validation
  rules. The concept doesn't cross a repo boundary, so it needs no citation.

## Tests

`internal/cmd/node_body_only_test.go`:

- byte-exact body-only output, including with no trailing newline
- the `get --body-only | update --content-file -` round trip
- header refusal from all three content sources, and the `--force` override
- the drop warning, compared against the stored (raw) body
- dry run: read-only, its JSON shape and its text output
- the dry-run refusals
- `spec get --body-only` is raw
- the `spec edit` refusal, `--force`, and warnings

`internal/cmdutil/bodyguard_test.go` covers detection and false-positive
cases.
