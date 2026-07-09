# Implementation Plan: `hadron ai-config create --file` — config from a JSON file

> **Status: implemented and verified**; this reflects the design as built. GH
> issue [#190](https://github.com/hadron-memory/hadron-cli/issues/190).

## Context

`ai-config create` could keep the API key off the command line only via
`--api-key -` (stdin), but every other field still had to go in argv. Agents and
provisioning scripts that already hold the whole config as JSON had to explode it
into flags, and the key rode along on stdin as a separate step. #190 asks for a
single JSON file that carries the config — key included — so nothing sensitive
touches argv or shell history.

No server change and no `make schema`: this is purely a client-side input path
onto the existing `createAiServiceConfig` mutation.

## Command surface (as built)

```
hadron ai-config create --file <path>|-   # whole config from JSON (or stdin)
hadron ai-config create --file cfg.json --model gpt-4o   # flag overrides a file field
```

The file is a JSON object whose keys mirror the flags — all optional:

```json
{
  "app": "acme.com:juno-app",       // or "agent" / "org" — exactly one owner
  "name": "default",
  "provider": "anthropic",
  "model": "claude-opus-4-8",
  "apiKey": "sk-...",                // omit for a key-less config
  "params": { "maxTokens": 4096 },  // an object, not key=value strings
  "enabled": true
}
```

## Merge semantics

- **File seeds every field; an explicit flag overrides the matching field.**
  Resolution is per-field via `strOr(fileVal, flagVal, changed)`.
- **Owner is overridden as a unit, not per-field.** Owner is a single choice
  (exactly one of `app`/`agent`/`org`), so if *any* owner flag is on the command
  line the file's owner selection is dropped wholesale and only the flags feed
  `resolveOwner`. A per-field merge would pair a file `agent` with a flag
  `--app` and trip the mutual-exclusion check (review fix).
- **Required fields** — `name`/`provider`/`model` are validated after the merge
  (from either source), so `MarkFlagRequired` was dropped in favour of a manual
  usage error; the file can now satisfy them.
- **`--param`**, when given, replaces the file's `params` object wholesale (the
  flag builds an object with `KeyValsToJSON`; there is no partial merge).
- **API key** — `--api-key` (including `--api-key -`) overrides the file's
  `apiKey`; otherwise a non-empty file `apiKey` is used. An absent/empty key is
  omitted from the mutation (key-less config), matching prior behaviour.
- **enabled** — defaults true; the file's `enabled:false` disables; `--disabled`
  forces disabled.

## Edge cases

- `--file -` reads the JSON spec from stdin. Passing both `--file -` and
  `--api-key -` is a usage error (both would consume stdin).
- Unknown top-level keys are rejected (`DisallowUnknownFields`) to catch typos
  before a config is created without a field the author intended. JSON's
  case-insensitive field matching still applies, so `apikey` is accepted as
  `apiKey` (but `api_key` is rejected).
- The file must be **exactly one JSON object**: trailing content after the first
  object (e.g. two concatenated objects) is rejected rather than silently
  ignored (the decoder's next token must be EOF).
- **`params`, when present, must be a JSON object** (matching the flag's
  semantics); a JSON `null` is treated as unset, and any non-object is rejected.
- File read / parse failures map to exit code 2 (Usage) via
  `exitcode.Newf(exitcode.Usage, …)`.

## Not in scope

`update --file` — the issue is about `create`. `update`'s omit-vs-clear
semantics (an explicit `null`/empty clears a field) don't map cleanly onto a
seed-and-override file merge, so it's left as a possible follow-up.

## Tests

`internal/cmd/aiconfig_cmd_test.go`: full config from a file (key never echoed),
flag-overrides-file, `--file -` from stdin, and unknown-field rejection. The
existing owner/required/key tests still pass unchanged.
