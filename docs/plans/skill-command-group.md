# Implementation Plan: `hadron skill` — export, status and lint for the skill surface

> **Status: in progress.** Written 2026-09-15 by Eli (skills-engineer) on
> Holger's dispatch, for [hadron-cli#580](https://github.com/hadron-memory/hadron-cli/issues/580);
> as of 2026-09-16 Eli is cleared for `hadron-cli` in full and owns #580 —
> both the command group and the corpus-side prerequisites in §9 (team chat
> seq #610/#611). Slice order follows §8a, which the coordinator reordered
> around the Micromentor onboarding path. Decisions marked **(Holger)** are his.

## Context

Hadron has 50+ runnable task nodes across 15 memories in 5 orgs, and no coherent
skill surface: 37 skills on disk in three naming families, no index, and an
export that is a *procedure an agent runs by hand*
(`hrn:node:hadronmemory.com:core:export-task-as-claude-skill`). Every generated
file carries `<!-- Edit the source node and re-export -->` and nothing enforces
it. #580 asks for a real export command; Holger ruled the exported prefix is
**`hadron-`, with a hyphen**, applied at export and never stored in the corpus;
Bo's product read ([comment](https://github.com/hadron-memory/hadron-cli/issues/580#issuecomment-5683466694))
widens the prefix to *per owning org*, replaces timestamps with content hashes,
fixes the set at three commands, and makes the exported skills the CLI plugin.

This plan takes all four of Bo's points, with one of them (the prefix) landing
as a decision for Holger because the platform has nowhere to store it yet.

## 1. What was measured before writing this

**The 20 Hadron-generated skills on `~/.claude/skills` today** (sourced from the
`Generated from` header), with their `description` length against the host's
limit:

| skill on disk | source loc (memory) | description |
|---|---|---|
| `start-worker-session-cli` | `hadron-cli:tasks:start-worker-session-cli` | **1983** |
| `start-worker-session-desktop` | `hadron-cli:tasks:start-worker-session-desktop` | **1592** |
| `end-worker-session` | `hadron-cli:tasks:end-worker-session` | **1186** |
| `add-hadron-platform-spec` | `specs:tasks:create-platform-spec` | **1126** |
| `add-spec` | `core:tasks:mint-spec` | **1089** |
| `mm-briefing` | `holger:holgers-assistant:tasks:mm-briefing` | **1059** |
| `create-node` | `core:tasks:create-node` | 876 |
| `publish-cli-release` | `core:tasks:publish-cli-release` | 861 |
| `finish-pr` | `ai-coding:finish-pr` | 790 |
| `drive-pr-home` | `core:tasks:drive-pr-home` | 770 |
| `hadron-create-task` | `core:tasks:create-task-node` | 535 |
| `hadron-export-task-as-claude-skill` | `core:export-task-as-claude-skill` | 491 |
| `extend-mm-app-hadron-memory` | `mm-app:tasks:extend-this-memory` | 441 |
| … 7 more, all under the limit | | |

Four facts fall out of that table and shape the design:

1. **The host limits are real and currently exceeded.** The skill spec caps
   `name` at 64 chars (kebab-case) and `description` at 1024 — verified against
   the official `skill-creator` validator (`quick_validate.py`, "per spec"), not
   guessed. Six of twenty exports exceed it. The host does not refuse them; it
   **truncates the description in the skill listing**, which is worse — the
   trigger phrases past the cut are silently invisible, so the skill stops
   firing on exactly the wording its author added last. Bo's "unverified today"
   is now verified, and it is a lint *error*, not a warning.
2. **Deriving the name from the loc is a rename of most of the set, not a
   validation.** Five of twenty disk names differ from their loc's terminal
   segment (`add-spec` ← `mint-spec`, `hadron-create-task` ←
   `create-task-node`, `extend-mm-app-hadron-memory` ← `extend-this-memory`, …).
   The rename pass Holger asked for is therefore the normal case of the first
   export, and the design pairs disk to corpus **by source URN**, never by name,
   so a rename is observed rather than inferred.
3. **A file on disk can point at a node that no longer exists.** My own
   `start-worker-session-desktop` skill carries
   `Generated from hrn:node:hadronmemory.com:hadron-cli:tasks:start-worker-session-desktop`,
   and that URN resolves to nothing (exit 4): the node was moved to `core`
   after the export, and the header kept the old address. (The first draft of
   this plan called that a fork in two memories; it is not — verified by
   reading both URNs, not by reading the header.) That is the `orphaned`
   drift class (§4.5) arriving on the author's own machine, and it is why
   `status` pairs by URN and `export` never deletes an orphan without
   `--prune` — an orphan is usually a node that moved. Real name collisions
   do exist in the corpus: `create-release-tag` is declared in both
   `marketrailz:market-railz-server` and `micromentor.org:mmdata`, and only
   the per-org prefix (D7) keeps them apart — which is why the collision rule
   runs on the prefixed name and skips nodes whose org has none.
4. **Sources span three roots** (`hadronmemory.com`, `micromentor.org`, `holger`),
   which is Bo's point 1 arriving as data: `hadron-mm-briefing` for a node owned
   by the `holger` root would name the wrong owner.

**The corpus, measured live with `hadron skill lint --all` as built (2026-09-16,
read-only against production, after the review rounds):** 14 memories hold
skill-declaring nodes; **63 findings** — 6 descriptions over the limit (the
same six as the disk table), 15 hand-set names that disagree with the derived
one, 27 declarations still under the legacy `claudeSkill` key, 3 declaring
nodes not marked runnable, 1 with an empty body, and 6 memories whose org has
no prefix (every org except `hadronmemory.com`). The first run reported fewer
(54 across 12) because `--all` then read only own-org and shared memories; the
PUBLIC listing was added in review. That first run also produced two false
classes the fakes could not have shown — a prefix-missing finding on 46
memories with nothing to export, and a "collision" between two prefix-less
orgs — both fixed before the verb shipped (§5.3).

Two corrections to the thread, measured on this machine:

- The **`h-task` / `h-search` / `h-open-node` "plugin skills"** are neither plugin
  nor skills: they are user-local slash commands in `~/.claude/commands/*.md`
  (hand-written, MCP-tool wrappers). Retiring them is a local cleanup, not a
  repo change, and nothing in this plan needs to touch them.
- There are **two plugins called "hadron"** on a developer machine. The CLI's
  own is `hadron-cli` (this repo: `.claude-plugin/marketplace.json` +
  `plugins/hadron-cli/`, one hand-written skill `use-hadron-cli`, decision
  D-2026-06-11-005). The other, `hadron@hadron` 1.0.0, is server-generated
  (station "Hadron Engineer", `localhost:3200` hooks, an `.mcp.json`) and is not
  this repo's. Bo's point 4 is about the first one only.

## 2. Decisions

### Resolved

- **D12. The declaration is `properties.exports.<host>` — and it SUPERSEDES D7,
  D8 and D10 below** (Holger, 2026-09-19, settled directly; other agents were
  paused). **Read this before D7/D8/D10; where they disagree, this wins.**

  ```json
  "properties": {
    "exports": {
      "claudeSkill": { "name": "hadron-add-copilot-reviewer", "description": "…", "enable": true },
      "codex":       { "…": "…" }
    }
  }
  ```

  An **object keyed by host**, not an array, so "one export per host" is
  structural and discovery stays a key check (`path: ["exports","claudeSkill"],
  exists`) rather than jsonb containment.

  | field | type | meaning |
  |---|---|---|
  | `name` | string | the skill name, **stored whole, prefix included** — no longer derived |
  | `description` | string | the host's retrieval text; same role and same 1024 cap as today |
  | `enable` | boolean | per-host on/off. **Must be `true` to publish; DEFAULTS TO OFF** (Holger, 2026-09-19) |

  *(`trigger` was considered for the text field and dropped: no new vocabulary
  for a thing already called a description.)*

  **Retires:** `Organization.skillPrefix` (the column, its `@unique`, its
  validation, `SKILL_PREFIX_TAKEN`, the SDL field), `--prefix`, `DeriveName`,
  the `Prefix{Value, Known}` pair, `LintPrefixes`, and two lint rules —
  `skill-prefix-missing` (6 live findings) and `skill-name-hand-set` (15), since
  a hand-set name is now the contract rather than a defect.

  **The cost, stated not argued.** With no prefix source, nothing can verify a
  stored name is *correct* — only that it is kebab-case and ≤ 64, so **a typo in
  the prefix lints clean**: `hadon-foo` passes every rule (and yes, that
  misspelling is deliberate — it is the whole example). The only evidence to hand is that this corpus already drifted
  when names were hand-set: 15 of 20 were hand-set, 5 contradicted their loc.
  Lint checks shape; consistency becomes a human convention.

  **What it fixes, and it is not small.** A stored name means **renaming a skill
  no longer touches the loc**, so D11's family rename is a property edit — no URN
  change, nothing to sequence. §11a's `id=` pairing still earns its place, but
  now only for **loc and memory moves** (the #490 re-home), never for renames.

  **`enable` DEFAULTS TO OFF and must be `true` to publish** (Holger,
  2026-09-19, reversing his earlier answer the same day). Publishing is the
  side-effecting act, so it takes an explicit opt-in. The reason is the corpus:
  it holds many runnable nodes that are **automation and were never meant to be
  skills at all**.

  Precisely: a runnable node with *no* declaration was never at risk — `isRunnable`
  has never been the selector, deliberately (§4.1). What the default protects
  against is a declaration arriving by a route nobody planned — a template, a
  bulk write, a half-finished edit — and publishing on the strength of merely
  existing.

  **A not-enabled declaration is the `disabled` class (§4.5), and `export`
  removes its file.** I proposed distinguishing an absent flag (leave the file)
  from an explicit `false` (remove it), so that flipping the default could not
  make a future export delete the 20 installed skills. **Holger ruled that out of
  scope**: there is no `export` command yet, so there is no first export to
  protect against, and the corpus is small enough to repair by hand. Recorded
  because the reasoning is worth having if the question returns at a larger scale
  — it was a guard designed for a command that does not exist.

  **Lint still judges a not-enabled declaration**, and the discovery predicate
  still does not filter on `enable`: a broken name is worth reporting BEFORE
  somebody turns it on, and `status` must be able to name a file whose
  declaration is switched off.

  `enable` lives in the **shared corpus**, so it disables for every reader of
  that memory. The per-machine *"not on my laptop"* case is still unaddressed.

  **There is NO data migration, and an earlier draft of this entry was wrong to
  claim an ordering constraint** (Holger, 2026-09-19: *"We don't need to migrate
  any task file. We just add the properties when all is set and done. There is no
  data that needs migrated."*). Verified, and he is right for a sharper reason
  than stated:

  **`skill lint` is the ONLY built consumer of a declaration.** `export` and
  `status` do not exist — the 20 skills on disk were written by the manual
  `export-task-as-claude-skill` procedure. So the 27 `claudeSkill` declarations
  are **27 diagnostic findings, not 27 pieces of load-bearing data**, and there is
  no pipeline that breaks by changing the key. My "migrate the nodes before
  dropping the column" was a constraint on a migration that is not happening; the
  column can be dropped whenever.

  So the order is just: **specs → code → add `exports` to nodes as they are
  touched.** Nothing is staged behind anything.

  **One thing to carry rather than migrate.** The 6 over-limit descriptions are
  @Holger's acceptance specimens for the truncation behaviour (§9.2). Their text
  also survives in the on-disk SKILL.md frontmatter, but `lint` reads the NODE —
  so when `exports.claudeSkill` is added to those six, **copy the description
  verbatim, overrun included.** Rewriting it to fit is how the test data quietly
  disappears.


- **D1. Prefix uses a hyphen** (Holger, 2026-09-15, on #580). `hadron_` stays
  the MCP tool namespace.
- **D2. ~~The corpus stores no prefix~~ — RESTATED by D12.** There is no prefix
  at all now, so nothing stores one. **What the corpus DOES store is the
  declaration**, name included (`properties.exports.<host>`). The old sentence
  *"nothing about this feature writes to a node"* meant the export COMMAND never
  writes, and that still holds — authoring a declaration is a human act, not
  something `export` does (@copilot on #627, who was right that the two readings
  needed separating).
- **D3. Three commands, no more** — `export`, `status`, `lint` (Bo). No `list`:
  `status` is the listing (§5.2), so the `list`/`ls` naming rule
  (`list_naming_test.go`) is not engaged.
- **D4. No scheduled auto-export** (Bo). `status` reports, `export` acts, and a
  human runs `export`.
- **D5. Staleness is content-addressed.** The generated header records a hash of
  the exact inputs the skill was built from; `status` compares hashes, never
  clocks (Bo, and §4.3).
- **D6. The batch read is the only read.** `NodeBatch` returns content raw;
  `GetNode` compiles Mustache and silently blanks `{{name}}`/`{{role}}`
  placeholders (`hadron-cli:findings:single-read-compiles-mustache`). The
  export never calls the single-ref read.

- **D7. ~~The prefix is stored per owning org, on the server~~ — RETIRED by D12**
  (Holger, 2026-09-19: too complex for one string). Kept below as the record of
  what shipped and what has to be removed. Was LANDED and deployed.
  **D7 as it stood:**
  Holger ruled it 2026-09-15; Dara shipped it the same evening as
  [hadron-server#1164](https://github.com/hadron-memory/hadron-server/pull/1164)
  (merged `1491106`, migration `20260915200000_org_skill_prefix`; **deployed** —
  `organization(ref:"hadronmemory.com"){ skillPrefix }` returns `"hadron-"` on
  production, seq #609). As built:
  `Organization.skillPrefix: String` (nullable, `@unique` — a duplicate refuses
  `SKILL_PREFIX_TAKEN`), validated `^[a-z][a-z0-9]*-$` so the stored value IS
  the literal prefix (`hadron-`, `mm-`), org-ADMIN writable on
  `updateOrganization` (explicit `null` clears, omitted preserves — the
  omitempty discipline applies if the CLI ever writes it), readable wherever
  the org is; `hadronmemory.com` is set to `hadron-` by the migration (mirrored
  in `post-push.sql`). GraphQL only — no MCP surface. Two cases:
  1. **Task in an org-owned memory → `Memory.organization.skillPrefix`.** If
     that is null the org has not chosen one: `export` **refuses** (`exit 2`,
     naming the org and the field) unless `--prefix` overrides. That refusal is
     the permanent rule for an unset org, not a stopgap — no client-side table.
  2. **Task in a user-owned memory (`organizationId` null) → `hadron-`**,
     fixed: a personal task has no org to name, so it takes the platform's.
  **CLI consequence (slice 2):** the committed snapshot predates #1164 and no
  memory operation projects `organization`, so this needs `make schema` from a
  sibling at `origin/main` ≥ `1491106`, then `organization { skillPrefix }`
  added to the `GetMemory`/`Memories` projections, then `make generate`. That
  is one schema refresh and one projection edit, not a new operation.
- **D8. ~~The skill name is composed, not stored~~ — RETIRED by D12**: the name
  is now stored whole in `exports.<host>.name`. Kept for the measurement that
  justified it, which still describes the risk D12 accepts.
  **D8 as it stood:** the skill name is composed, not stored: `<prefix>` + the task's loc
  slug** (§4.2). Today every exported node carries a hand-set
  `properties.claudeSkill.name` that duplicates — and in five of twenty cases
  contradicts — what the loc says. "Retire" means: the node stops storing a
  name at all; the exporter derives it. During transition a hand-set name is
  accepted only if it equals the derived one (lint error otherwise); after the
  rename pass the key is removed from every node. The **description stays a
  property** — it is authored trigger text and cannot be derived.
- **D10. The marker and the exporter are provider-neutral — SUPERSEDED IN FORM
  by D12**, which keeps the intent and changes the shape: multi-host is now
  `exports.<host>` rather than one `skill` key plus `--host`.
  **D10 as it stood:** Holger: this has to
  work for other AI hosts (Codex, …), whatever they call a skill. So the opt-in
  marker becomes **`properties.skill`** (`{description, …}`), and the existing
  `claudeSkill` key is read as a legacy alias during transition and then
  removed alongside `name`. The export takes `--host claude|codex|…` (default
  `claude`), each host being one renderer over the same node inputs and the
  same header contract — the node body is host-agnostic, only the wrapper
  differs. **Only the Claude Code renderer is specified here**; what Codex (and
  any later host) actually reads — file name, location, frontmatter, limits —
  must be verified against that host's current documentation before its
  renderer is built, not assumed from memory. `status`/`lint` are host-aware
  only where a limit is host-specific (the 64/1024 caps are Claude Code's).

- **D11. The worker-skill family is renamed for symmetry, and the renames are
  HELD behind `id=` pairing** (Holger, 2026-09-19 — both halves ruled).

  ```
  hadron-cast-worker     ← tasks:cast-worker    (NEW, landed 2026-09-19)
  hadron-start-worker    ← tasks:start-worker   (the two start tasks MERGED)
  hadron-end-worker      ← tasks:end-worker     (from end-worker-session)
  ```

  Holger proposed `hadron-start-worker`; the symmetry follows from it, because
  shipping `hadron-start-worker` beside `hadron-end-worker-session` would
  **destroy** the guessability the whole epic is for — a reader who meets one
  could not guess the other. That mismatched pair is the anti-pattern this
  decision avoids, not an option. **The desktop/CLI split is merged into
  one task**: `end-worker-session` already covers both tracks in one node, so
  the asymmetry was unjustified; track selection is something the skill can
  determine at runtime; and 129 characters of the CLI task's own description are
  spent saying *"NOT for Claude Desktop … use the desktop track instead"* — a
  trigger surface partly devoted to not triggering.

  **Measured, and correcting an argument made for this in chat:** the
  mutual-disambiguation prose is only ~104 and ~184 characters, so **merging
  does NOT fix the 1024 overrun.** One merged description must still cover two
  surfaces. The overrun's real cause is that these descriptions enumerate
  gotchas — body and abstract content — rather than triggering.

  **~~The HOLD is the load-bearing half~~ — DISSOLVED by D12** (@copilot on #627
  caught that this was left standing). Under D12 the name is **stored**, so
  renaming a skill is a property edit: no loc change, no URN change, nothing for
  §11a's pairing to lose. **The renames are unblocked and need no sequencing.**

  What §11a's `id=` pairing still earns its place for is a **loc or memory move** —
  the [#490](https://github.com/hadron-memory/hadron-cli/issues/490) re-home — which
  really does change the URN. That half of the hold stands; the rename half does
  not. Kept as the record of why the hold existed, since the reasoning applies
  again the moment anything moves a loc.

  This is not hypothetical — it already happened once. The single orphan in the
  2026-09-19 sweep is `start-worker-session-desktop`, whose node was moved from
  `hadron-cli:` to `core:` as the partial fix for
  [#490](https://github.com/hadron-memory/hadron-cli/issues/490) and whose disk
  header kept the old address.

  **Do the re-homing in the same pass** — [#490](https://github.com/hadron-memory/hadron-cli/issues/490)
  (open, filed at Holger's request 2026-08-18) wants the onboarding tasks out of
  a memory named after a tool the reader may never use. `start-worker-session-cli`
  and `end-worker-session` are still in `hadron-cli:`. Both that move and this
  rename are loc changes blocked on the same mechanism.

  **The destination memory keeps the name `core`** (Holger, 2026-09-19) — #490's
  last open item. So there is no third wave of loc changes and the migration is
  fully sequenced: **`id=` → re-home + merge + rename, one pass.**

  He added that *"the memory content needs a cleanup, but that's another task"*,
  and the measurement shows why the name felt wrong in the first place:
  **`core` contains a `server:` branch.** Of its 29 nodes, `server:` (7) and
  `shared:` (12) have gone untouched since 2026-08-02 while `tasks:` (8) is the
  live stratum. The name reads like server internals because the memory
  literally holds server-architecture docs — so the fix was content, not naming.
  Tracked on [#438](https://github.com/hadron-memory/hadron-cli/issues/438) and
  **explicitly out of scope here**: it does not block D11.

- **D9. Selection is every exportable task the caller can read — for EVERY
  target, the plugin bundle included. RULED 2026-09-22 (Holger), and it
  REVERSES the proposal.** The proposal kept one narrower rule: the bundle
  committed to this public repo could carry only `visibility = PUBLIC`
  memories, because a customer's private tasks cannot ship in a public
  artifact. Holger: *"this is all accessible task nodes, including those in
  PUBLIC … I didn't think this through properly. Let's get it through with this
  for now, and then we can follow up with a way to curate the exported list."*

  **What the proposal conflated.** `--to plugin` names two different artifacts:
  a USER's own bundle in their own checkout, and OUR bundle committed here. A
  visibility filter is plainly wrong for the first — it would silently drop a
  user's own private tasks from their own plugin — and the proposal attached it
  to the COMMAND, so it applied to both.

  **Measured before the ruling**, so the cost of each side was a number:
  `--all --to plugin` resolved to 28 declaring tasks unfiltered and **12**
  filtered; the filter dropped 16, of which 13 were customer or personal
  (`micromentor.org:*`, `marketrailz:*`, `holger:holgers-assistant`) and
  **three were first-party Hadron tasks** whose memories are merely
  ORGANIZATION rather than PUBLIC (`add-hadron-platform-spec`,
  `update-hadron-docs`, `distill-imported-page`).

  **Consequence accepted, deliberately and on the record:** with no filter,
  running `--all --to plugin` in THIS checkout stages customer and personal
  tasks into a public repo, caught only by whoever reads the diff. Holger
  ruled to ship without a guard for now.

  **The follow-up is a SCOPE**, not a restored filter (Holger): curation keys
  on a named scope — the platform already has scopes as named sets of memories
  — so a bundle declares what it carries rather than inferring it from
  visibility. That also answers the three first-party drops, which a visibility
  rule could only fix by making those memories public.

### Proposed — for Holger

*(none outstanding; D9 was the last and is now resolved above.)*

## 3. Command surface

```
hadron skill export  (-m <memory>... | --all | --node <ref>...) [--host <host>] [--to user|project|plugin|<dir>] [--prune] [--dry-run] [--json]
hadron skill status  (-m <memory>... | --all)                   [--host <host>] [--to user|project|plugin|<dir>] [--strict] [--json]
hadron skill lint    (-m <memory>... | --all | --node <ref>...)                       [--strict] [--json]
```

> **Updated by D12.** `--prefix` is gone with the prefix itself. **`--host` takes
> the property key verbatim** — `claudeSkill`, not `claude` — because the host id
> and the storage key must be one vocabulary or `--host claude` and
> `exports.claudeSkill` need a mapping table nobody maintains (@copilot on #627).
> `claudeSkill` is the default and the only host with a specified renderer.
>
> **`lint` takes no `--host` and checks EVERY host entry**, tagging each finding
> with the host it came from. A node declaring only `exports.codex` is therefore
> linted for shape — a malformed entry is reported — but the Claude-specific caps
> (64/1024) apply only to the `claudeSkill` entry, since they are that host's
> limits. As shipped in #589 lint validates the `claudeSkill` entry only; the
> all-host walk lands with the second renderer (@codex on #627).

- `--host` selects the renderer and the host's root/limits (D10); `claudeSkill` is
  the default and the only one specified in this plan. It lands with `export`
  and `status` (the verbs that render or read a host's files); `lint` as
  shipped in #589 has no host-specific behavior and takes no `--host` — the
  64/1024 caps it enforces are documented as Claude Code's, and a second
  host's limits arrive with its renderer.

- `-m/--memory` is repeatable; `--all` is every memory the caller can read —
  three listings, each drained with `api.CollectAll` and every memory class
  named explicitly (a nil filter hides agent-system memories): own-org
  (`Memories`), shared with you (`MemoriesSharedWithMe`), and other orgs'
  PUBLIC memories (`Memories` with `visibility: PUBLIC`), de-duplicated by
  id. That is what the server LISTS; a per-user agent memory
  (`userMemoryOfAgentId`) is excluded from `memories()` by contract and no
  filter surfaces it, so `--all` does not promise it — lint one with `-m`.
  One of the three selectors is
  required (`exit 2` otherwise) — no active-memory fallback, because an export
  that silently targets "whatever memory was active" is how a customer's tasks
  end up on the wrong disk.
- `--to` names the skills root: `user` (default) → `~/.claude/skills`;
  `project` → `<git toplevel>/.claude/skills`; `plugin` →
  `<git toplevel>/plugins/hadron-cli/skills` (§6); anything else is a directory.
  `project`/`plugin` outside a git worktree is `exit 2`.
- `lint` runs on the corpus and touches no disk; `status` reads both and writes
  nothing; `export` is the only writer. Bo's line: *lint on the corpus, status on
  the disk, export bridges them.*
- Exit codes follow `spec lint`: an ERROR finding ⇒ `Conflict` (5) via
  `exitcode.Silent`; warnings alone exit 0; `--strict` promotes warnings to
  errors. `status` exits 0 with drift reported unless `--strict`, so a CI
  drift gate is `hadron skill status … --strict`.

## 4. The model

### 4.1 Selection: what is a skill-declaring node

> **SUPERSEDED BY D12 (2026-09-19).** The selector is now
> **`properties.exports.<host>` with `enable: true`** — an object keyed by host,
> carrying `{name, description, enable}`. The discovery predicate becomes
> `path: ["exports","<host>"], exists`, and `enable: false` is a DECLARED but
> disabled node (§4.5 `disabled`), not an undeclared one. The paragraph below
> describes the retired shape; the pagination, batch-read and `unavailable`
> mechanics under it are unaffected.

A node is in the export set iff **`properties.skill` is an object** (D10; the
legacy `properties.claudeSkill` is honored as an alias during transition). That
is the existing opt-in and stays the only one — "declared, not merely runnable"
is what stops a stray runnable node being published. `isRunnable` is a lint
precondition (§5.3), not the selector.

Discovery is server-side and deliberately NOT an `isRunnable` scan (which
would hide exactly the nodes the not-runnable rule exists to catch): ONE
`findNodes` over every selected memory id with a `where` predicate —
`properties.skill` exists OR `properties.claudeSkill` exists (#719; verified
to hold on the server) — paged to exhaustion at 500 rows, under the server's
2000-row clamp so a short page really is the end. Bodies then come through
one `api.CollectNodeBatch` fan-out over the listed ids (200-node / 1 MB
chunks, spillover re-queued), `unavailable` surfaced as a warning and never
dropped. `--node` refs are canonicalized and de-duplicated locally; a bare
loc or a scheme-prefixed ref of another kind is a usage error before any
request.

### 4.2 Name derivation — RETIRED by D12

> **The name is STORED, in `exports.<host>.name`, prefix included.** There is no
> derivation, no `--prefix`, and no `Organization.skillPrefix`. What survives is
> the **validation**: a name must match `^[a-z0-9]+(-[a-z0-9]+)*$` and be ≤ 64
> code points — now applied to the stored value rather than a derived one, and a
> violation names the node rather than the loc. The algorithm below is kept as
> the record of what it replaced.


```
skillName(prefix, loc):
  segs := split(loc, ":")
  if segs[0] == "tasks" && len(segs) > 1 { segs = segs[1:] }   // the tasks: branch is structure, not name
  return prefix + join(segs, "-")
```

`tasks:create-release-tag` → `hadron-create-release-tag`;
`export-task-as-claude-skill` (root-level) → `hadron-export-task-as-claude-skill`;
`tasks:review:run` → `hadron-review-run`. Validation after derivation: matches
`^[a-z0-9]+(-[a-z0-9]+)*$`, ≤ 64 chars — a loc that derives to an invalid name is
a lint error naming the loc, never silently munged.

### 4.3 The generated file

```markdown
---
name: <properties.exports.<host>.name (D12) — STORED, not derived from the loc>
description: <properties.exports.<host>.description (D12) — after NormalizeDescription>
---

<!-- hadron-skill id=019d4811ff23757da5ba8f8cff6f281f source=hrn:node:hadronmemory.com:core:tasks:create-release-tag hash=3f9a1c02b7e4d5a6 -->
<!-- Generated by `hadron skill export`. Edit the source node and re-export; do not edit this file. -->

<node content after NormalizeBody: CRLF folded, surrounding newlines trimmed, inner content untouched>
```

> **Both frontmatter values come from the DECLARATION, not from the loc** (@copilot
> on #627). A concrete example would read `name: hadron-create-release-tag` for the
> node `core:tasks:create-release-tag` — and that *looks* derived, which is exactly
> the misreading to avoid: since D12 the two coincide only because whoever authored
> the declaration chose a name matching the loc. Nothing enforces it, and a skill
> may legitimately be named something its loc does not suggest.

- One **machine-parseable header line**, `<!-- hadron-skill k=v k=v -->`, parsed
  by a small regex; the second line is prose for humans and is not parsed.
  Existing files carry the older `<!-- Generated from <urn> -->` form; `status`
  reads that too (URN only, no hash ⇒ reported as `unhashed`, which `export`
  upgrades). **A file with `source` but no `id`** is the pre-§11a generation:
  also `unhashed`, also upgraded by the next export (§4.4, §11a).
- **`id`** is the node's stable primary key and is **the pairing key** (§4.4,
  amended per §11a). It survives a `loc` change, which the URN does not.
- **`hash`** = first 16 hex of SHA-256 over `id + "\x00" + source + "\x00" +
  name + "\x00" + description + "\x00" + content` — **and when `id` is empty it
  is SKIPPED, contributing neither a field nor a separator**, so the digest is
  byte-identical to the pre-§4a formula `source + "\x00" + name + …` (ruled
  2026-09-22; see §11a for the measurement and why the earlier
  hash-the-empty-string rule was retired). This is the ONE statement of the
  formula — the pairing key and the
  source node URN plus the three rendered inputs (description and body in their
  normalized form). Both addressing fields are included so a hand-edited header
  naming another node reads as a local edit rather than pairing the file with a
  node it was never rendered from; **`id` most of all, since it is the field
  that decides pairing** (§11a records the earlier draft that excluded it, and
  why that was wrong). It is deliberately NOT
  `nodedoc.ContentHash` alone (8 hex over content only): a description edit
  must read as stale, because the description is the trigger. Since every input
  is present in the file itself, the hash is **recomputable from the file
  without the server**, which is what makes "locally edited" detectable (§4.5).
- Frontmatter carries exactly two keys and goes through the real YAML encoder
  (`nodedoc.MarshalYAML`, the `go.yaml.in/yaml/v3` dependency nodedoc already
  has) and is read back with the same library — never hand-quoted: a
  description ending in a colon is a plain scalar a hand check passes and a
  real parser rejects, and the skill host IS a real parser (review round 1).
  Not `nodedoc.RenderMarkdown`, whose header is the node round-trip shape.
- **One normalization, shared by lint, render, hash and parse:**
  `NormalizeDescription` (surrounding whitespace trimmed) and `NormalizeBody`
  (CRLF folded to LF, surrounding newlines trimmed). The length lint
  certifies is the length on disk; a body wrapped in blank lines, or
  authored on Windows, hashes equal to its own header.
- **Provenance is recognized only in the preamble** immediately after the
  frontmatter, and only as the exact lines the renderer writes (the machine
  line by its full `key=value` grammar, the human lines verbatim, the legacy
  line only with a URN-shaped token). A foreign skill that quotes our header
  in its body stays foreign; a body that opens with its own HTML comment
  keeps it. The parser's preamble strip is the exact inverse of `Render`.

### 4.4 Pairing disk to corpus: by node ID, never by name

> **Amended 2026-09-19 (§11a).** This section previously said *"by URN, never by
> name"* and called the URN the only pairing. **The node `id` is the pairing
> key; the URN is provenance.** The URN does not survive a `loc` change and the
> id does, so URN-pairing turns a rename into `orphaned` + `never-exported`
> (§11a has the measurement). The *name*-related half of the old rule is
> unchanged and is the point: a name is never a pairing key.

`status` and `export` walk `<root>/*/SKILL.md` and index each file by the
**`id`** in its header. The corpus side is indexed by the same node id. A name
and a URN are then both *properties* of a pairing, so a rename is the
observation "same id, different directory" and a re-home is "same id, different
URN" — neither is a guess.

The header also carries a **flat v2 node URN** (`hrn:node:<root>:<slug>:<loc>`)
as human-readable provenance and as a hash input (§4.3). That is the ONLY
spelling recognized: measured on every generated skill on disk, all 20 headers
already carry the flat form, and this surface supports no v1 (Holger,
2026-09-16) — a `::` or `urn:` header is not ours and stays in the file's body.

**A file with no `id`** is a pre-§11a header: pair it by URN as a fallback and
classify `unhashed` so the next export upgrades it. That fallback is what makes
the upgrade possible at all, and it is exactly why **every installation must
export once before any loc changes** — see §11a's rollout, which is a real
ordering constraint rather than a nicety.

Non-Hadron skills (no header) are invisible to every command — `hadron skill`
never lists, moves or removes a file it did not generate.

### 4.5 Drift classes

| class | how it is known | `status` | `export` |
|---|---|---|---|
| `current` | on disk, hash(node) == header hash == hash(file) | ✓ | skip (idempotent) |
| `stale` | hash(node) ≠ header hash, hash(file) == header hash | ✓ | rewrite |
| `locally-edited` | hash(file) ≠ header hash | ✓ (warning; error under `--strict`) | **refuse** unless `--force`; the edit is someone's work |
| `renamed` | URN paired, directory name ≠ derived name | ✓ | write new dir, remove old, report `moved` |
| `never-exported` | declared in corpus, no file | ✓ | write |
| `orphaned` | file's URN resolves to no declared node (deleted, un-declared, or unreadable) | ✓ | leave; remove only with `--prune` |
| `unhashed` | pre-#580 header (URN only) | ✓ | rewrite with hash |
| `collision` | two declared nodes derive one name under one prefix | ✓ | **refuse the pair**, export the rest |
| `unavailable` | listed but unreadable (`nodeBatch.unavailable`) | ✓ | skip, report |
| `disabled` | declared but not enabled — `enable` absent or `false` (D12; it defaults to OFF) | ✓ | **file present:** remove it, no `--prune` needed. **file absent:** nothing to do. **file `locally-edited`:** REFUSE unless `--force` — see below |

**`locally-edited` OUTRANKS `disabled`** (@codex P1 and @copilot on #627, both
right, and the two rules genuinely contradicted each other as written). A
disabled declaration whose file has been hand-edited is **refused unless
`--force`**, exactly as a `stale` one is. A1 exists to protect somebody's work,
and `enable` lives on a SHARED node — so the person switching it off is often not
the person who made the local edit. Destroying their work on the strength of
someone else's toggle is precisely the outcome A1 was ruled to prevent.

**`disabled` covers a declaration with NO file too** (@codex on #627). §5.2
promises one status row per declared node, and a not-enabled declaration is still
declared, so it gets a `disabled` row whether or not a file exists. **The CLASS is
about the declaration; the ACTION is about the file.** `never-exported` would
contradict the disablement, and leaving it unclassified would silently drop a row
§5.2 promises.

`locally-edited` is the class Bo's list did not name and the hash makes free:
without it, `export` would overwrite a person's hand-fix with a stale node and
call it success. `orphaned` is never deleted by default because an orphan is
usually a node that *moved*; `--prune` is the deliberate act.

## 5. The three commands

### 5.1 `skill export`

1. Discover + batch-read (§4.1). *(D12 removed a prior step here: there is no
   prefix to resolve, because the name is stored.)* Lint the set first (§5.3) — a node that fails
   lint is **not exported** and is listed in the result; `export` never writes a
   file it would then report as broken.
3. Walk the target root (§4.4), classify (§4.5), act per the table.
   `--dry-run` prints the same report with nothing written.
4. Write atomically (temp file + rename in the skill dir) so a crash mid-set
   leaves no half-file.
5. Report: one row per node — `written | moved(from) | skipped(current) |
   refused(reason)` — plus `orphaned` files and the reminder that the host
   loads skills at session start. `--json` shape:
   `{root, written: [...], moved: [{from,to,urn}], skipped: [...],
   refused: [{urn, reason}], removed: [{urn, name, reason}], orphaned: [...],
   pruned: [...]}` with every slice initialized to `[]`.

   **D12 changed two things here.** `prefix` is gone from the shape — there is no
   prefix. And `removed` is new: it reports a file deleted because its declaration
   is not enabled (§4.5 `disabled`), with `reason` naming which of the two cases
   it was. Without it a `disabled` removal would be an unreported side effect, and
   `--dry-run` could not show it (@copilot on #627).

### 5.2 `skill status`

Same discovery and walk, no writes. The table view is the answer to *"what can
Hadron do for me?"* — one row per declared node with skill name, source URN,
class — and a per-memory footer counting **runnable nodes that declare no
skill**, so the corpus's unexported surface is visible without being exported.
`--strict` exits 5 on any drift; that is the CI gate for the plugin (§6).

### 5.3 `skill lint`

Corpus-only rules, each naming the node and the fix. A node is selected by the
discovery predicate; **`properties.exports.<host>` is the declaration (D12)**, with
`properties.skill` and `properties.claudeSkill` read as legacy aliases during the
migration:

| rule | level |
|---|---|
| `skill-declaration-malformed` — `exports` not an object, a host entry under it not an object, a `description`/`name` that is not a string, or an **`enable` that is not a boolean** (D12). Reported even beside a valid key: the node is mid-migration, and the broken key is the one that outlives the alias | error |
| `skill-description-missing` | error |
| `skill-description-too-long` — > 1024 **characters** (code points, as the host's validator counts), measured on the normalized text, with the overrun stated | error |
| `skill-name-invalid` — the **stored** name (`exports.<host>.name`) not kebab-case or > 64 code points. D12: it is stored, so the DECLARATION is what to change, not the loc | error |
| `skill-name-missing` — a declaration with no `name`, or an empty one. D12 retired derivation, so there is nothing to fall back to (an empty string is judged, not read as absent) | error |
| ~~`skill-name-hand-set`~~ — **RETIRED by D12**: a stored name IS the contract. Nothing can verify its prefix; `skill-name-invalid` still checks shape | — |
| `skill-not-runnable` | error |
| `skill-content-empty` / `skill-content-has-frontmatter` (a COMPLETE `---…---` block; a leading horizontal rule is a body) | error |
| ~~`skill-prefix-missing`~~ — **RETIRED by D12** (no org prefix exists to be missing) | — |
| `skill-name-collision` — two selected nodes **store** one name. D12: this now fires ACROSS orgs, because no prefix keeps two orgs' identically-named tasks apart — the cost D12 accepts. A declaration with no stored name is excluded (`skill-name-missing` is its finding; pairing two nameless nodes as colliding on `""` would report one defect twice) | error |
| `skill-legacy-key` — declared under a **retired top-level key** (`skill` OR `claudeSkill` — both are legacy relative to `exports.<host>` since D12), or a retired key left beside `exports`. @copilot on #627: a node using only `properties.skill` was previously treated as current, which it is not | warning |
| `skill-description-no-trigger` — no "use when" phrasing | warning |
| `skill-content-has-template` — a `{{…}}` placeholder; export is verbatim | warning |
| `skill-node-unavailable` — listed but unreadable (not found, or not readable by you — the server's merged envelope, cor:api:040) | warning |

## 6. The plugin target (Bo's point 4)

`--to plugin` writes to `plugins/hadron-cli/skills/` in the current checkout,
carries **every accessible task node** with no visibility filter (D9, ruled
2026-09-22 — curation is a later feature and will key on a named scope), and
leaves `use-hadron-cli` alone — that skill is hand-written on purpose (`hadron-cli:claude-plugin`'s
keep-the-skill-thin rule: it defers to `hadron agentic-usage` so it cannot
drift; the task exports are procedures, a different kind of skill, and drift is
exactly what `status` exists to catch).

Drift gate: a `skill-drift` workflow (nightly, like `schema-drift`) runs
`hadron skill status --all --to plugin --strict` against the committed plugin.
The repo already holds a Hadron read token — `secrets.HADRON_TOKEN`, used by
`memory-hygiene.yml` to read `hadronmemory.com::hadron-cli` — so no new secret
is needed.

**But D9's reversal changes what that token DECIDES, and the gate must say so
(@copilot on [#655](https://github.com/hadron-memory/hadron-cli/pull/655)).**
Under the retired PUBLIC-only rule the selection was a property of the CORPUS,
so any adequately-scoped token saw the same set. With no filter, `--all` is
"every memory the CI IDENTITY can read" — so the token's scope IS the
selection. Two consequences the gate has to state rather than inherit:

- **A maintainer running the same command locally sees a different set**, because
  they can read memories CI cannot. The gate would go green on a bundle a human
  had just been told was stale, or red on tasks nobody can see. Neither failure
  announces itself.
- **Widening the token silently widens the gate** — including over customer
  memories, which is the exposure D9's reversal already accepts elsewhere.

So the workflow must **pin its selection explicitly** rather than rely on
`--all`: name the memories with `-m`, or (once curation lands) name the scope.
`--all` is right for a human asking "what does my disk look like"; it is wrong
for a gate, which has to compare the same two things every night. Whoever
builds the workflow states the CI identity and what it can read, in the
workflow file, next to the command.

**Not** a local re-export in CI, because the
plugin changing under a maintainer's hands is Bo's "nothing not to build"
arriving through a side door. `plugin.json` version bumps when the generated
set changes (existing rule).

## 7. Out of scope

- Any write to a node. The corpus stays the source; this is one-way publishing.
- Other skill hosts (Cursor rules, etc.) — the header format carries no
  host-specific key so a second target can be added without breaking `status`.
- Deduplicating the forks — Eli's corpus work (§9). The command refuses the
  collision; it does not resolve it.
- The `h-*` slash commands — local files, not this repo's.

## 8. Implementation slices (Jonas)

1. **`internal/skilldoc` (pure, no cobra) — SHIPPED in #589, and three of these
   are since REMOVED by D12** (`DeriveName`, `LintPrefixes`, `Prefix`; see D12
   and §11). The list below is the record of what #589 built, not a current
   inventory: **`DeriveName`,
   `Hash`, `Render`, `ParseFile` (both header generations), `Lint`,
   `LintPrefixes`, `LintCollisions`, `Prefix{Value, Known}`. `Classify` (the
   drift classes, §4.5) lands with `status`. Table-driven unit tests:
   derivation cases incl. root-level locs and nested `tasks:a:b`; hash
   stability; round trips through the real YAML parser on awkward
   descriptions, CRLF, leading comments and lookalike provenance lines;
   header parse of the old `Generated from` form.
2. **Discovery + the prefix read — SHIPPED in #589:** one `findNodes` over
   every selected memory id with the declaration predicate, `--all` over the
   three listings via `api.CollectAll`, one `CollectNodeBatch` fan-out,
   `unavailable` surfaced. The schema snapshot was refreshed from
   hadron-server `b3d79d7` and `organization { skillPrefix }` added to the
   `GetMemory` / `Memories` / `MemoriesSharedWithMe` projections
   (`MemoriesSharedWithMe` gained `$memoryClasses`). That refresh also
   regenerated `scopeRef` on the schedule/webhook inputs WITHOUT `omitempty`
   — fixed in the same PR and captured as
   `findings:a-schema-refresh-can-regress-an-unrelated-write`.
3. **`skill lint`** — SHIPPED in #589 (seven Codex rounds + one Copilot round; twelve findings fixed on-thread).
4. **`skill status`** — the walk, the pairing, the report, `--strict`.
5. **`skill export`** — the writer, `--dry-run`, atomic writes, rename pass,
   `--prune`, `--force` for `locally-edited`.
6. **Plugin target + CI gate + docs:** `--to plugin`, the `skill-drift`
   workflow, `agentic-usage.md` surface line (`agentic_completeness_test.go`
   fails without it), README, the `doc-map` surfaces, this plan updated to
   *as built*, and a `hadron-cli` memory node + preflight route for the
   header/hash contract.

Command tests use `testFactory`/`captureGraphQL` with a fake `findNodes` +
`nodeBatch` keyed by operation name, and a `t.TempDir()` skills root; every
test asserts the user's exit code (`exit_code_assertion_test.go`). Flag usage
strings: no back-quoted words except placeholders (`flag_usage_test.go`);
required flags say so (`required_flags_help_test.go`).

## 8a. Delivery order (coordinator, seq #611 — supersedes §8's order, not its content)

The customer is the Micromentor team, whom Holger wants driving agents; the
onboarding path they would walk has a hole (no task for standing up a team or
casting a worker) and the two session-ritual skills they meet first are among
the six over the description limit. So:

1. **`skill lint`** (§8 slices 1–3) — no disk, and it catches the overruns
   mechanically.
2. ~~**Fix the three session-ritual descriptions**~~ — **MOVED TO LAST**
   (Holger, 2026-09-19): the overruns are the acceptance specimens for the
   truncation behaviour, so they stay broken until `status`/`export` can be
   demonstrated against them. See §9.2.
3. **Write the missing task — cast a worker / stand up a team**, folding in the
   mint-vs-bind guard (the miscast on day one) and the traps in
   `hadron-cli:findings:team-rebuild-under-worker-model`.
4. **`skill export` + `status`** (§8 slices 4–6) — **but see §11: the
   2026-09-16 ruling moved the judgment server-side, so these are no longer
   hadron-cli slices as §8 describes them.**
5. **The corpus survey** — after, not before. *(Partly done 2026-09-19: the
   50-node runnable survey and the orphan sweep are in §1.3 and §9.1.)*
6. **Shorten the six descriptions** — the former step 2, now that the
   commands it is test data for exist.

One worktree per worker (hadron-cli#472): Eli works in a worktree of his own,
never in Jonas's checkout; branches are name-prefixed (`eli/…`); pickup is
announced in the team chat before the first edit.

## 9. Corpus prerequisites and the first export (Eli)

Before the first `export --all` on Holger's machine can be clean:

1. **Fix the one `orphaned` header, and resolve any real forks the survey
   finds.** There is **no** `start-worker-session-desktop` fork — §1.3 has the
   verification and this item used to contradict it. One node exists
   (`core:tasks:start-worker-session-desktop`); the disk header points at
   `hadron-cli:tasks:…`, which resolves to nothing. That is an **orphan**, not a
   canonicality judgement.
   Swept 2026-09-19: **19 of 20** on-disk provenance URNs resolve, **1**
   orphan — this one. Real forks, if the survey finds any, are superseded and
   annotated, never deleted.

   **It needs an explicit repair and the next export does NOT perform one** —
   this item previously claimed it did, corrected on @copilot's review. The file
   pairs by nothing (no `id`, unresolvable URN) so it is `orphaned`, and export
   leaves an orphan alone without `--prune`; the node meanwhile is
   `never-exported` and gets a fresh directory. The export **adds** a second
   directory rather than fixing the first. §11a names the two acceptable
   repairs.
2. **Shorten six descriptions — ON HOLD (Holger, 2026-09-19).** They are the
   acceptance specimens for the truncation behaviour: they are the only
   `skill-description-too-long` population in the live corpus, so fixing them
   first leaves `status`/`export` with no real input to prove the class
   against. **Do not "fix the lint findings" — the six overruns are test
   data until `status` and `export` can be demonstrated on them.** Shorten
   them after, without losing trigger phrases (the ones past the cut are the
   ones the host is dropping today anyway). This reorders §8a, which had the
   shortening at step 2.
3. **Set `isRunnable`** on every declared node that lacks it.
4. **Drop `claudeSkill.name`** from every node once D8 is ruled, or set it to
   the derived value during transition.
5. **The first export IS the rename pass**: `hadron skill export --all --to user
   --dry-run` shows every `moved(from)`; the real run does them. Then delete the
   three `~/.claude/commands/h-*.md` by hand.

## 10. Open questions for Holger

1. ~~D7 — prefix home~~ **Ruled and landed** (hadron-server#1164, `1491106`):
   `Organization.skillPrefix` + `hadron-` for user-owned tasks (§2).
2. ~~D8 — derived name~~ **Ruled:** prefix + task slug; `claudeSkill.name`
   retired (§2). D10 (provider-neutral marker, `--host`) follows from his
   Codex requirement and is stated, not yet confirmed.
3. ~~D9~~ — **RESOLVED 2026-09-22**: no visibility filter on any target; the
   plugin bundle carries every accessible task node, and curation becomes a
   named scope later (§2).
4. `locally-edited` (§4.5): when a generated `SKILL.md` has been edited by
   hand since export, should the next `export` **refuse** to overwrite it
   (recommended — the edit is someone's work and the refusal tells them to
   move it into the node or pass `--force`), or **overwrite and report** (the
   node is the source, literally)?
5. ~~CI token~~ **Resolved:** `secrets.HADRON_TOKEN` already exists
   (`memory-hygiene.yml`); only its scope needs checking (§6).

## 11. The domain logic moved to hadron-server — what this does to §8

**Ruled by Holger, 2026-09-16** ([hadron-server#1177](https://github.com/hadron-memory/hadron-server/issues/1177)):
`internal/skilldoc` moves to hadron-server and the CLI becomes a thin client.
The stated reason is that skills are core rather than convenient — a CLI-only
implementation is unreachable from MCP and the portal, and most users will
never install a CLI, so the capability would exist for almost nobody.

**The split: the client does I/O and rendering; the server does judgment.**
For `lint`, the client reads the `SKILL.md` off disk and sends its content;
the server returns the verdicts. The server never needs the caller's disk.

**§8's slices 4–6 predate this and are written as hadron-cli work. They are
now wrong about where the code goes, and so are the issues filed from them:**

| | as filed | under the ruling |
|---|---|---|
| [cli#620](https://github.com/hadron-memory/hadron-cli/issues/620) | `Classify` — the nine drift classes — "lands here" | `Classify` is **judgment**; it goes server-side |
| [cli#621](https://github.com/hadron-memory/hadron-cli/issues/621) | the writer, rename pass, `--prune` | writer/rename/prune are **I/O**; they stay |
| [srv#1177](https://github.com/hadron-memory/hadron-server/issues/1177) | port `skilldoc`, Eli specifies | **specified 2026-09-19 — Dara's** |

**Slices 1–3 are affected too, and saying "4–6" understated it — @copilot's
review, correct.** `internal/skilldoc` IS slice 1, shipped in
[#589](https://github.com/hadron-memory/hadron-cli/pull/589), and the ruling
moves it. Its status, stated so nobody has to infer it:

| | under the ruling |
|---|---|
| `internal/skilldoc` (634 lines, slice 1) | **TRANSITIONAL.** It is the reference implementation the TS port is checked against, then it is **deleted.** |
| `internal/cmd/skill/` (slice 3's cobra wiring) | **RETAINED** — disk I/O and presentation, the client half. |
| `skill lint` the command (slice 3) | **RETAINED as a thin client**: same verb, same exit codes, verdicts fetched instead of computed. |
| the shared fixtures extracted from `skilldoc_test.go` | **PROMOTED** — they outlive the Go package as the parity contract (#1177 §2). |

So #589 is not wasted and is not permanent: it is the specification-by-code the
port is graded against. **Do not delete the Go package until the TS side
reproduces its hashes on the 20 real files on disk** — that is #1177's A2
acceptance, and deleting early removes the only oracle.

cli#620 and cli#621 were filed 2026-09-19, three days *after* the ruling, and
neither references it. #620 in particular places the drift classification in
the client, which is the one part #1177 most clearly moves — its own text says
*"the same split covers `export` and `status`, which are corpus reads and sit
even more naturally server-side."*

**So the next step is not a slice of CLI code — it is the #1177
specification, which is mine and is not written.** It is named in Dara's queue
(seq 781, 787, 806) and #1177 has zero comments. Nothing server-side can start
without it, and building #620 as filed means writing in Go the thing the
ruling schedules for TypeScript.

**The split line I propose, for the spec to state precisely** — derived from
§4.2–§4.5 rather than re-decided:

- **Server (judgment, from `skilldoc`):** `ValidName` (§4.2), `Hash` (§4.3),
  `Render` (§4.3), `Lint`/`LintCollisions` (§5.3), `Classify` (§4.5). These are
  pure functions over corpus inputs plus, for `lint`/`Classify`, one file's
  content and header — all of which a client can send.

  **D12 removed three from this list** (@codex and @copilot on #627, who caught it
  still assigning them): `DeriveName`, `LintPrefixes` and D7's prefix resolution
  do not move server-side because they no longer exist. `ValidName` survives and
  validates the STORED name. On the server side this landed as
  `derive.ts` → `name.ts` in hadron-server#1225.
- **Client (disk I/O + terminal presentation):** the disk walk, reading a
  `SKILL.md`, `ParseFile` *or* sending raw content for the server to parse,
  atomic writes, the rename's directory move, `--prune` deletions, `--dry-run`,
  the report table and `--json` DTOs, `--strict`'s exit code.

  **Two different things are called rendering, and #1177's sentence uses the
  other one.** The issue says *"the client does I/O and rendering"*, meaning the
  client renders the **report a human reads**. `Render` in §4.3 — composing the
  `SKILL.md` body and frontmatter — is *authoring the artifact* and is server
  side, because it is the thing whose bytes the hash is taken over. Server
  renders the **file**; client renders the **view**. Stated because the
  ambiguity can send an implementer to the wrong repository.

- **The seam is the drift class:** the client sends
  `{nodeId, sourceUrn, headerHash, fileHash, dirName}` per file plus the target
  selection; the server returns a class per node from §4.5 and the rendered
  body for anything to be written.

  **`nodeId` is the primary pairing key and `sourceUrn` is the fallback** for a
  pre-§11a header that carries no id (§4.4). Sending only the URN would
  reproduce the §11a defect exactly — a file whose node has moved arrives with
  an obsolete URN and classifies `orphaned` while the node classifies
  `never-exported` — so the id travels or the seam is broken.

  Pairing on the server keeps §4.4's definition on the side that owns it, and
  leaves the client unable to invent a class.

~~**Open, and for Holger** (see §10)~~ — **`locally-edited` RULED = refuse**
(Holger, 2026-09-19). It is assumption A1 of the #1177 spec. It mattered before
the seam rather than alongside it, because once `Classify` is server-side
`locally-edited` is *a class the server returns*, and **stop** versus **proceed
and mention it** is a property of the contract rather than of a verb.

## 11a. Pair on the node ID, not only the URN

**Found 2026-09-19 by Holger asking whether the worker-session skills could be
renamed — a question about naming that turned out to be a question about
pairing.** Amended into the [#1177 spec](https://github.com/hadron-memory/hadron-server/issues/1177#issuecomment-5744955718)
as its §4a before anything was built against it.

§4.4 **used to say** *pair by URN, never by name*, and the reason given — a
rename of the derived NAME becomes an observation rather than a guess — is
sound, and the name half of it still stands. What the URN half does not survive
is the other kind of rename (§4.4 is now amended to match this section, rather
than left contradicting it):

| what changes | URN | node id | class under URN-pairing | should be |
|---|---|---|---|---|
| derived name (prefix, host) | stable | stable | `renamed` ✅ | `renamed` |
| **the node's `loc`** | **changes** | **stable** | `orphaned` + `never-exported` ❌ | `renamed` |

**Measured, not quoted.** `hadron node move --help` states it — *"keeping each
node's id, so every incoming and outgoing edge reference stays valid"* — but the
committed schema snapshot carries no description on `moveNode` at all, so the
guarantee is documented on the client surface rather than in the contract. So it
was driven instead:

```
node add  experiments:eli-move-probe-a   → id 01a0bb967bf77dd7967feec7c8cd61af
node mv   …probe-a --to-urn …probe-b     → ✓ moved
node get  …probe-a                       → ABSENT        (control: the move ran)
node get  …probe-b                       → id 01a0bb967bf77dd7967feec7c8cd61af
```

Scratch node deleted. **The id is immutable across a loc move; the URN is not.**
So a loc rename silently splits one skill into a dead file plus a fresh export —
and because an orphan is never removed without `--prune` (deliberately), the user
ends up with two directories carrying near-identical trigger text, both firing.

*(That the guarantee lives only in CLI help and not in the SDL is itself worth a
server-side note — a client-documented invariant is one a second client may not
know it can rely on. Reported, not filed.)*

**The fix is one more header key:**

```
<!-- hadron-skill id=01a0099f949d76a9baf3a16527485475 source=hrn:node:… hash=… -->
```

- **Pair on `id`** (§4.4, amended); keep `source` as human-readable provenance
  *and* as a hash input.
- A loc move then pairs, and the URN mismatch makes the hash differ, so it
  classifies **`stale`** → rewrite, which updates the header's `source`. Correct
  by construction, no tenth class.
- Existing files carry no `id`. `unhashed` already means *"an older header
  generation, rewrite it"* — **widen that class** rather than invent. A1 is
  unaffected: a header-generation upgrade is not a local edit.
- **An EMPTY id is SKIPPED, not hashed as the empty string** — it contributes
  neither a field nor a separator, so the digest is byte-identical to the
  pre-§4a formula.

  **Corrected 2026-09-22 (Holger's ruling), after @Dara measured that the rule
  we had both published did the OPPOSITE of the rationale we gave for it.** The
  rationale — *"a pre-§4a file stays self-consistent"* — was right. Hashing `""`
  defeats it, because prefixing `"" + \0` is not a no-op:

  ```
  legacy (pre-§4a, id absent)      4b02a08bb1efbaad
  hash-the-empty-string (retired)  283c6ac5b51c3cd7   ← not equal
  ```

  The consequence was not a wrong label. `locally-edited` **outranks** every
  other class, so every hash-bearing legacy file would have become one that
  `export` REFUSES without `--force` — and §4b's rollout is *"run export once so
  headers gain `id=`"*. The rollout could not have happened, and A1 would have
  been protecting work nobody did.

  Skipping delivers the stated rationale: a legacy file recomputes to exactly
  the digest already in its header, so it is **not `locally-edited`** and an
  ordinary export rewrites it. That IS the quiet rollout.

  **Which class it does get is unsettled, and deliberately not asserted by the
  client** (@copilot on [#654](https://github.com/hadron-memory/hadron-cli/pull/654)).
  §4.3 and §4.4 above both say **`unhashed`**; @Dara, reading `classifySkill`,
  described it falling through to **`stale`**. Both carry the action *rewrite*,
  so the rollout is safe either way — which is likely why the disagreement went
  unnoticed. It is the server's vocabulary (A4), so it is raised for the server
  side rather than decided here.

  **Caught before it cost anything** because [#621](https://github.com/hadron-memory/hadron-cli/issues/621)
  had not shipped, so nothing had ever WRITTEN a `hash=` header — measured at
  zero affected files on two machines. The fix was free exactly once.

  **Why no test caught it:** every fixture was built by `Render`, so the suite
  only ever asserted that this package's writer and reader agree — which they do
  under either rule. The discriminating case is the generation no current code
  path can produce, so `TestAnEmptyIdReproducesThePreSection4aDigest` spells the
  pre-§4a formula out independently.

- **The hash INCLUDES `id`.** The formula is stated once, in §4.3 — not restated
  here, because two spellings of one formula is how the separators go missing.

  **Corrected 2026-09-19 on @codex's P2, and the reason I first gave was
  self-inconsistent.** I wrote *"the hash must NOT include `id`, so it stays
  recomputable from a file whose header a human has retyped"* — but `source`
  lives only in the header too and **is** hashed, precisely so that *"a
  hand-edited provenance line naming another node reads as a local edit rather
  than pairing the file with a node it was never rendered from"* (§4.3). The
  id is now the field that decides pairing, so it needs that protection more
  than `source` does, not less.

  Left out, the failure is concrete: edit **only** the header's id and
  `hash(file)` still equals `headerHash`, so the file reads as untouched while
  pairing to a **different node** — classified `stale` rather than
  `locally-edited`, and therefore overwritten or moved despite A1's ruled
  refusal. Every hash input remains present in the file, so recomputability is
  untouched.

### The rollout is an ordering constraint, not just a code dependency

**Added 2026-09-19 on @codex's P1 and @copilot's medium — both right, and the
original "`id=` lands, then rename" was underspecified.** *Landing* the
id-aware code is not the precondition; **every installation having exported
once under it** is:

```
1. id-aware code ships                         ← necessary, NOT sufficient
2. each installation runs `skill export`        ← headers gain id=  (THE precondition)
3. only then: the loc changes (D11 + #490)
```

Skip step 2 anywhere and that installation still holds a no-`id` header. After
the move its URN no longer resolves either, so it pairs by **neither** key —
which recreates the orphan-plus-duplicate this section exists to prevent, on
the one machine nobody checked.

So `skill status` must be able to answer *"is this root fully upgraded?"* — a
count of `unhashed` files is exactly that signal, and **zero of them is the
green light for step 3.** Under `--strict` it already exits non-zero, so the
gate is a command rather than a promise.

**The alternative, considered and not chosen:** a durable old-URN → id bridge
in the corpus, so a stale header can be repaired after the fact. It costs a
persistent mapping that would have to outlive the rename forever, to save a
one-off export that `status` can verify. Recorded so the choice is visible; if
an installation is ever found unable to run step 2, this is the fallback to
build.

### Already happened once — and the existing orphan needs a REPAIR, not an export

The single orphan in the 2026-09-19 sweep (19 of 20 provenance URNs resolve) is
`start-worker-session-desktop`, moved from `hadron-cli:` to `core:` as the
partial fix for [#490](https://github.com/hadron-memory/hadron-cli/issues/490),
disk header left pointing at the old address.

**§9.1 previously said the next export corrects it mechanically. That was wrong
— @copilot's medium, confirmed against §4.5/§5.1.** Walk it: the file has no
`id` and an unresolvable URN, so it pairs by nothing → `orphaned` → and export
**deliberately leaves an orphan alone** without `--prune`. Meanwhile the node at
`core:` pairs to no file → `never-exported` → written to a fresh directory. The
export therefore *adds* a second directory rather than fixing the first, and
both carry near-identical trigger text.

**It is the one file no upgrade path reaches**, because step 2 above can only
upgrade a header whose URN still resolves. Its repair is explicit and one of:
delete the stale directory by hand before the first export, or run
`export --prune` once and accept that `--prune`'s blast radius is every orphan
in that root. **Whichever is chosen, it is a named step and not a side effect** —
and it is the concrete argument for the ordering above, since after D11 lands
there would be three more of these.

**Consequence for D11 and #490:** every remaining rename or re-home is a loc
change, so all of them are held behind this. Cheap now, a migration later.
