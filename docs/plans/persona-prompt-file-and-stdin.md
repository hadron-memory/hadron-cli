# `--persona-prompt-file` / stdin for the CLI's longest text field (#541)

Design-as-built for hadron-cli#541.

## The footgun

`agent create` / `agent update` took the persona prompt inline only. It is the
longest text this CLI accepts — the nine `hadron-dev-team` role agents run
1772–1902 words — and it is markdown, so it is dense with exactly what a shell
argument handles worst: backticks (inline code), `{{name}}` braces, `$`, quotes,
and blank-line paragraph structure. The most natural content is the most
dangerous:

```sh
hadron agent create … --persona-prompt "You own the corpus. Use `spec new` to allocate a citation."
#                                                              └─ the shell runs `spec new` before hadron sees the string
```

The failure is silent: the agent is created with a prompt that is missing text
and may have absorbed command output.

## The fix, and what it reuses

`cmdutil.ResolveTextInput` already implements the inline / `--<flag>-file` /
`-`-stdin convention — it was added for issue #38 and is used by `spec`,
`node`, `chat`, `object`. The persona prompt is simply the field that never got
wired to it. So this change is mostly plumbing, not a new mechanism.

Added to **both** `agent create` and `agent update`, for **both** long-text
prompts (the issue asked for `--system-prompt` too — identical shape):

- `--persona-prompt-file <path>` / `--system-prompt-file <path>`
- `--persona-prompt -` / `--system-prompt -` reads stdin
- each inline flag and its `-file` sibling are `MarkFlagsMutuallyExclusive`
  (and `ResolveTextInput` refuses the pair as a second layer, so the guarantee
  holds even if the cobra registration is dropped)

### The one new guard: stdin is one stream

Two text fields sharing one stdin is new here — no single-field command had the
problem. If both prompts were `-`, the first read would drain stdin and the
second would silently get `""`. `refuseMultiStdin` counts the `-` sentinels and
refuses more than one **before either read**, so the clash is a usage error
rather than a silent empty field.

### Preserve-vs-clear on update

`update` keeps `changedStr`'s contract: a field is sent only when the caller
passed it, so an unset flag preserves and an explicit empty clears.
`resolvePromptFlag` extends that to the flag PAIR — it returns `nil` (preserve)
only when neither `--persona-prompt` nor `--persona-prompt-file` was passed, and
otherwise a pointer to the resolved value. `create` keeps `optStr` (an empty
resolved value is omitted, unchanged).

## Deferred deliberately: `$EDITOR`

The issue also floats opening `$EDITOR` on `agent update --persona-prompt` with
no value, mirroring `spec edit`. Left out on purpose:

- it is a "consider", not the reported footgun;
- it needs a `GetAgent` read first to pre-populate the buffer with the current
  template, plus the TTY-detection and editor-seam machinery `spec edit` has —
  materially more surface and test burden than the file/stdin flags;
- `--persona-prompt-file` already makes the ~1800-word edit safe:
  `hadron agent get <ref> --json | jq -r .personaPrompt > f && $EDITOR f && hadron agent update <ref> --persona-prompt-file f`.

Worth a follow-up if the round-trip proves common; not blocking #541's actual
complaint.

## Verification

Rendered `--help` read (the only check for the backticks-become-placeholder
trap): the placeholder shows `string`, and the `-` in "a lone - reads stdin" is
prose, not a back-quoted word, so it does not rewrite the flag shape.

Live, dev binary, offline server so the failure is provably local:

| input | exit |
| --- | --- |
| `--persona-prompt a --persona-prompt-file /tmp/x` | 2 (cobra mutual-exclusion) |
| `--persona-prompt - --system-prompt -` (piped) | 2 (only one flag reads stdin) |
| `--persona-prompt-file /tmp/does-not-exist` | 2 (reading --persona-prompt-file: …) |

Tests assert the wire var, not a decode: a template with backticks, `{{name}}`,
`$(rm -rf /)` and quotes reaches `personaPrompt` byte-for-byte through
`--persona-prompt-file`, and through `--persona-prompt -`. Two mutants, each
built and each turning its named test red: dropping `refuseMultiStdin`, and
dropping `resolvePromptFlag`'s preserve short-circuit.

## Not here

- **No `--json` change** — the resolved value still maps to the existing
  `personaPrompt` / `systemPrompt` variables; the agent DTO is untouched.
- **No new operation or error code.**
- **No spec** — client-side input ergonomics over an existing contract; a
  reader in another repo needs to know nothing new.
