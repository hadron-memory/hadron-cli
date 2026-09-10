# Refuse an App ref client-side, in the CLI's own words (#540)

Design-as-built for hadron-cli#540.

## The symptom, and where each half of it came from

```
$ hadron team worker list --app hadron-dev-team
hadron: input:3: workers URN "hadron-dev-team" is not fully qualified. Expected a app URN with at least 2 hierarchy segments (org::app-slug, e.g., "acme.com::dev-app").
```

Four defects in one line, and none of them is in this repo's code:

| defect | origin |
| --- | --- |
| `input:3:` | genqlient renders a GraphQL error as `<file>:<line>: <path> <message>`; the CLI printed `err.Error()` |
| `workers URN` | the same renderer's `<path>` — the field the ref landed in, not the flag the caller typed |
| `org::app-slug`, `acme.com::dev-app` | urn-lib-js `MIN_SEGMENTS_HINT`, which spells every type's example in the retired `::` grammar |
| `a app` | the hint's article is fixed while the type word is interpolated |

The CLI already shape-checks memory refs (`CanonicalMemoryRef`) and node refs
(`ResolveNodeRef` / `CanonicalNodeURN`) before the round trip. App refs were
forwarded verbatim from every site, so the server's refusal was the only
refusal — and it is written for a GraphQL caller, not for someone at a
terminal. An error message is read at the moment the reader decides what to
type, and it is the most-copied text in a CLI; for the half of this CLI's
audience that is an agent, it is authoritative.

## The fix

`cmdutil.CanonicalAppRef(what, ref)` — the App counterpart of
`CanonicalMemoryRef`, with one deliberate difference: an unrecognised shape is
**refused** (exit 2) rather than passed through for the server to reject,
because the server's rejection is the thing being fixed.

Accepted, and normalised to `hrn:app:<root>:<slug>` on the wire (emit v2,
accept everything — #239/#697):

| typed | sent |
| --- | --- |
| `acme.com:dev-app` | `hrn:app:acme.com:dev-app` |
| `acme.com::dev-app` (legacy, never advertised) | `hrn:app:acme.com:dev-app` |
| `hrn:app:acme.com:dev-app` / `hrn:app:acme.com::dev-app` / `urn:app:…` | `hrn:app:acme.com:dev-app` |
| a PK — cuid (`c` + 24) or 32-char hex | verbatim |

Refused, naming the source (`--app`, `--install-into`, `<app-ref>`, `<app>`,
`<value>`, or `the configured App context`) and listing only the v2 forms:

```
--app "hadron-dev-team" is not org-qualified — name the App as hrn:app:<root>:<slug> (canonical, e.g. hrn:app:acme.com:dev-app), the <root>:<slug> short form, or an App id
--app "acme.com:a:b" does not name an App — expected hrn:app:<root>:<slug> (canonical, e.g. hrn:app:acme.com:dev-app), the <root>:<slug> short form, or an App id
```

The bare-slug case gets its own first clause because that is the #540 shape
and it reads as "I forgot the org"; the forms clause is one constant shared by
both, so the two cannot drift.

### Parity with the server, and where it deliberately diverges

The id shapes mirror hadron-server's entity-ref dispatcher exactly
(`src/lib/entityRef/shape.ts` `isId`, spec 007): anything not cuid- or
hex-shaped is treated as a URN there too, so a bare slug never resolved and
refusing it loses nothing. Unlike `IsNodeID`, the cuid shape IS admitted: the
reason `noderef.go` refuses it — a cuid is indistinguishable from a loc — has
no counterpart for App refs.

Two places the client is stricter than `assertFullyQualifiedUrn(ref, 'app')`:

- **exactly two atoms**, where the server's gate checks a minimum of two. A
  three-atom ref passes the server's qualification and then fails lookup as
  "not found"; it can never name an App, so the shape message is the better
  answer.
- **atom charset** (`ValidateAtomShape`), which the server applies one step
  later in `parseUrnInput`. Same outcome, earlier and in the CLI's words.

Neither refuses anything that resolves today.

## Every site, enumerated

The guarantee this change makes — *no App ref reaches the server unchecked* —
is quantified over paths, so the paths are listed rather than implied. Sources
of an App ref, and where the check sits:

| source | sites | check |
| --- | --- | --- |
| persistent `--app` / configured context | `Factory.App()` — consumed by `team chat/worker/role/session/init`, `app agent list`, `ai-config list`, and every `ResolveAppRef` fallback | `Factory.App()` |
| local `--app` on the headless-run commands | `run trigger/list`, `schedule create/list`, `webhook create/list`, `task run` | `ResolveAppRef` |
| local `--app`, forwarded directly | `ticket mint`, `node import --task`, `memory attach`, `memory set` (App-scoped create), `connection grant create`, `ai-config create` (via `resolveOwner`) | at the site |
| positional | `app agent list [<app-ref>]`, `app agent add/remove <app>`, `app uninstall <app-ref>` | at the site |
| `--install-into` | `agent create` — before `CreateAgent`, so a bad target cannot cost an orphan agent (the #543 rule) | at the site |
| the direct `f.AppFlag` read | `team session list` provenance chain (`-m` outranks `--app` there, so it cannot go through `Factory.App()`) | at the site |
| writes into the config | `app set-active`, `config set app` | before the write; the canonical form is what is stored |

The mechanical check, which is what the table was built from and what a
reviewer should re-run rather than trusting the table:

```sh
grep -rn 'appRef\s*:\?= \|appRef, err :\?= \|AppRef:\s*[a-zA-Z&]\|AppRef = \|\.AppRef = ' internal/cmd --include='*.go' \
  | grep -v '_test.go' \
  | grep -v 'CanonicalAppRef\|ResolveAppRef\|f\.App()\|describeApp\|appRef: appRef\|AppRef: appRef\|AppRef: resolved\|appRefArg'
```

Every remaining line must originate from a server-returned id (`b.AppID`,
`appForTeamMemory`, `scope.Ref` from `resolveTeamAppScope`). It did.

`TestAppRefRefusedClientSideBeforeAnyRoundTrip` walks 22 of those invocations
with the issue's literal input and a fake server that has NO responses, so a
site that forwards a ref is a request the test reports by operation name.

### Sites that swallow `f.App()`'s error

`team chat` (chat.go:67), `session start --as` and the provenance chain call
`f.App()` as `if ambient, aerr := f.App(); aerr == nil && ambient != ""`. A
hand-edited malformed config therefore degrades to the next fallback on those
paths instead of refusing. Stated rather than changed: those sites treat a
failed config load the same way today, the CLI's own writers now refuse to
store such a value, and every non-swallowing path names the remedy
(`hadron app set-active <ref>`).

## Consequences worth knowing

- **The wire form is canonical.** Tests that asserted `appRef ==
  "acme.com:eng-team"` now assert the `hrn:app:` form. `--json` fields that
  echo the ref (`agent create --install-into`'s `install.appRef`, `app
  set-active`/`app uninstall`'s `app`) carry the canonical form rather than
  the spelling typed. Keys unchanged; the value is now one spelling.
- **`URN_NOT_QUALIFIED` → exit 2.** The server's typed refusal (spec 022) is
  a caller-fixable input, so `codeForExtension` maps it to Usage for every ref
  the CLI still forwards unchecked. Measured: `memory get definitely-not-a-memory`
  exits 2 where it exited 1. Its message is unchanged and still the server's
  — that is #338 §3, the memory-ref instance of this same fix.
- **`task run` checks its arguments before its round trip.** `--arg` parsing
  and `--app` used to sit below the node-ref resolution, so a usage error cost
  a `ResolveUrn` request. Pure reorder.
- **Fixtures.** Tests passed `app1`, `app2`, `app_1` as App refs — values the
  server would refuse as unqualified too. They are now cuid-shaped
  (`capp1000…`), replaced consistently including in the JSON fixtures that
  return them, so ids still pass through and assertions still match.

## Verification

Live, against prd with the dev binary (read-only):

| input | result |
| --- | --- |
| `--app hadron-dev-team` | exit 2, the new message, no request |
| `--app acme.com:a:b` | exit 2, "does not name an App" |
| `--app hadronmemory.com:hadron-dev-team`, `…::…`, `hrn:app:…`, `urn:app:…::…` | exit 0, scope line shows `hrn:app:hadronmemory.com:hadron-dev-team (from --app)` |
| `--app <32-hex id>` | exit 0, same scope line |
| `app agent list hadron-dev-team` | exit 2, `<app-ref> "hadron-dev-team" …` |

Exit codes were read with `$?` on a redirected command, not through a pipe —
zsh's `$?` after `| head` is head's.

Mutation checks. Each mutant was applied by a script that asserts the target
text occurs exactly once before rewriting (a no-op probe throws), then built,
then tested; the red count is top-level tests, all in the files this change
adds or edits:

| mutant | build | reds |
| --- | --- | --- |
| a bare slug passes through (`IsAppID(ref) \|\| !strings.Contains(ref, ":")`) | ok | 4 |
| `Factory.App()` forwards the flag verbatim | ok | 18 |
| drop the `URN_NOT_QUALIFIED` case | ok | 2 |
| `task run` resolves the node before `--app` again | ok | 2 (the table test's `task run` row and its parent) |

## Not done here, and why

- **The `input:N: <path>` rendering leak is a class, not this instance.**
  Three sites already strip it locally (`asset` scan refusals, `node import`
  edge rejections, `team` worker-not-found) and #338 §3 is a fourth. The
  shared fix is in `api.MapError`'s rendering; it is a separate change with a
  wider blast radius and its own issue.
- **The `::` hint is urn-lib-js's**, shared by every server refusal for every
  type. Not this repo's to fix; reported to the coordinator.
- **No spec.** The accepted forms are the server's existing contract (spec 007
  entity refs, spec 022 qualification); this is one client implementing it
  ahead of the round trip. Nothing here decides a rule another repo must match.
