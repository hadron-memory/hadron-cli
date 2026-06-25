# Spec flag-name audit (#99 item 5)

The issue reported guessing `spec get --scope all` (doesn't exist) and that
flag names aren't consistent across `spec` subcommands. This is the audit, plus
the low-risk fixes it warranted.

## Inventory

| Subcommand | Distinctive flags |
|---|---|
| `ls` / `get` | `--prefix`, `--limit`, `--offset`; `get` adds `--abstract-only`, `--body-only` |
| `find` | `--match-exactly`, `--tag`, `--limit` |
| `lint` | `--product`, `--module`, `--all`, `--strict` |
| `new` / `extract` / `edit` | `--content`/`--content-file`, `--abstract`/`--abstract-file`, `--tag`, `--no-edges`, `--dry-run` |
| `describe` | `--declare` |
| `supersede` | `--feature`, `--rule-after`, `--reason`, `--copy-body`, `--yes` |
| all | `--memory/-m`, plus the persistent `--json`/`--server`/`--app` |

## Findings

Most apparent "inconsistencies" are different *concepts*, correctly named
differently, and shouldn't be unified:

- **Scope.** `ls`/`get` scope by `--prefix` (a citation prefix); `lint` scopes
  by `--product`/`--module`/`--all`. These select different things (a node
  subtree vs. a lint corpus) and read naturally in place. The guessed
  `get --scope all` has no real analogue — `get` operates on one citation or one
  prefix, never "everything". No change.
- **View filters.** `get --abstract-only`/`--body-only` are a paired view
  filter, distinct from the `--content`/`--abstract` *value setters* of
  `edit`/`new`. The pairing is intentional; renaming half of it would break the
  symmetry.

The one **genuine vocabulary drift**: `edit`/`new`/`extract` call the markdown
body **content** (`--content`, `--content-file`), but `get` calls it **body**
(`--body-only`). An author who learned `--content` there reasonably guesses
`--content-only` here.

A second, smaller papercut: `find --match-exactly` is verbose; `--exact` is the
obvious short guess.

## Fix — non-breaking aliases, no renames

Renaming a shipped flag breaks the agent-facing contract (the flags are
documented in `agentic-usage.md` and scripted by agents), so the canonical
flags are unchanged. Instead a small `withFlagAliases` helper (spec.go) wires
accepted aliases through the pflag normalizer:

- `get --content-only` → `--body-only`
- `find --exact` → `--match-exactly`

An alias resolves to its canonical flag at parse time; unknown flags still
error, and the canonical names remain the help-listed, documented surface (each
flag's usage string names its alias). Adding an alias later is one map entry.

## Tests

`TestWithFlagAliases` proves an alias sets its canonical flag and that unknown
flags are still rejected.
