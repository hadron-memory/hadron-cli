# Design proposal: `hadron` plugin export — an installable multi-skill bundle (#653)

> **Status: proposal, not built.** Written 2026-09-24 by Jonas (cli-engineer) on
> Ada's dispatch (team chat #1365) as the bounded design-and-handoff slice of
> [cli#653](https://github.com/hadron-memory/hadron-cli/issues/653).
> **Jane implements**, after milestone 1 (individual skill-file export, #621)
> ships. This document builds nothing, releases nothing, and touches none of
> Jane's #621 acceptance or matrix files. Where a question has more than one
> defensible answer it is listed in §7 **without being chosen**; decisions
> marked *(ruled)* cite who ruled and where.

## 1. What this is for

#653 exists to replace three second-hand claims with measurements:

1. Codex takes the same plugin format as Claude.
2. Cowork installs a multi-skill plugin, and a plugin is the way to get many
   skills in at once.
3. The per-skill zip shape (folder inside the zip, `SKILL.md` with `name` and
   `description` frontmatter).

portal#890 has since checked all three **against vendor docs** (§3), and
shipped a per-host download. What nobody has done yet is **install** one. So
the CLI producer is still the instrument #653 described: done means *a bundle
that actually installed*, verified by installing it (§6), not a bundle that
matches our reading of the docs.

It is also an output mode in its own right. Individual skill files remain the
CLI's power-user affordance (`hadron skill export`, #621); the plugin is the
unit a user installs (`cor:agt:030:03`), and the producer that builds
hadron-cli's own committed plugin is one invocation of it (plan
`skill-command-group.md` §6).

## 2. Contracts this rests on

| Contract | What it fixes for this command |
|---|---|
| [`cor:agt:030:00`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:00) | Node is authoritative, file derived, nothing flows back. **Run to the end:** an item is one skill for one host; one failing never stops the others; the end-of-run report names every failure and every declaration nothing could export. **No prompting** mid-run. |
| [`cor:agt:030:01`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:01) | The rendered file is derived from the node; provenance header. |
| [`cor:agt:030:02`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:02) | User-level destinations, one per host, every known host unconditionally; **no repo-level destination; never dependent on the working directory or a checkout.** See §7 Q1 for how an explicit output directory sits against "one destination per host". |
| [`cor:agt:030:03`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:03) | A plugin is one installable unit carrying many skills. **Selection:** every enabled task the caller can read, for every target; no visibility filter; **narrowed only by a scope** (never by memory picks or named nodes). **Format, layout and manifest are deliberately outside the contract** — so §3 of this document is implementation and may change without superseding anything. |
| [`cor:agt:030:05`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:05) | A skill over its host's limit is out of export (an item failure, reported). |
| [`cor:agt:030:06`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:06) | Only an enabled declaration exports; name collisions are item failures. |

Rulings already made and not reopened here *(ruled)*:

- **Server-rendered bodies and judgments** — the client renders nothing and
  invents no class or action (Ada on #653, 2026-09-24; same seam as #621).
- **Canonical hosts** `claudeSkill` / `codexSkill`; `SkillPlanInput.host` is
  singular, so two hosts are two plans with separately reported
  `scanned`/`judged` (Dara, measured on `8bbae1d`).
- **Explicit output, no git requirement** (plan §6 note, B8; Ada on #653).
- **Exit 5 after the full report** when any item was refused or failed
  (Holger, #1326) — the #621 rule, reused.
- **Do not infer a shared Claude/Codex format**; actual target installation is
  the acceptance (Ada on #653).

## 3. Target packaging, per host — checked separately

Read from each vendor's own docs on **2026-09-24**, with the command names
checked against the installed Claude Code 2.1.143 and codex-cli 0.156.1. The facts below are
**volatile by design**: `cor:agt:030:03` keeps vendor formats out of the
contract, so this section may be corrected without superseding anything.
**UNCONFIRMED** marks a claim no official source states. The portal's earlier
check (2026-09-23) is
[`hadron-portal:findings:skill-download-artifact-shape-per-host`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-portal:findings:skill-download-artifact-shape-per-host);
where this section adds to it, it says so. The additions are also recorded as
[`hadron-cli:findings:plugin-hosts-have-user-level-locations-and-version-gates-updates`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-cli:findings:plugin-hosts-have-user-level-locations-and-version-gates-updates).

### 3.1 Claude Code

Sources: [plugins reference](https://code.claude.com/docs/en/plugins-reference),
[plugins](https://code.claude.com/docs/en/plugins),
[plugin marketplaces](https://code.claude.com/docs/en/plugin-marketplaces).

```
<plugin>/
├── .claude-plugin/plugin.json        # only the manifest lives here
└── skills/<skill-name>/SKILL.md      # every component dir at the plugin ROOT
```

- **`plugin.json`:** optional. When present, only `name` is required, and it
  must be kebab-case. Optional fields include `version`, `description`,
  `author`, `homepage`, `repository`, `license` and `keywords`.
- **`version` controls updates.** If it is set, users only receive an update
  when it is bumped. This makes the version the one piece of metadata the
  producer cannot leave as a constant (§7 Q6).
- **Skills are namespaced** `/<plugin-name>:<skill-name>`, so the plugin
  name is user-visible.
- **Lasting installs go through a marketplace.** A marketplace is
  `<root>/.claude-plugin/marketplace.json`. It requires `name`,
  `owner.name` and `plugins[]`, and each entry requires `name` and a
  `source`. A relative `./` source is the local form.
- **Installing a local marketplace needs no git.** Run
  `claude plugin marketplace add <dir>` (or `/plugin marketplace add ./dir`),
  then `claude plugin install <plugin>@<marketplace>`.
- **`--plugin-dir <dir|zip>` loads a plugin for one session only.** It is
  useful for acceptance, and it is not an install.
- **A folder under `~/.claude/skills/` carrying `.claude-plugin/plugin.json`
  loads as `<name>@skills-dir`**, with no install step.
  - This is a **user-level location with no marketplace**, which matters for
    §7 Q1.
  - It shares a root with #621's per-skill files, which matters for §7 Q9.
  - Documented, but not yet observed by this team.
- **`claude plugin validate <path>`** validates plugins and marketplaces,
  and is a cheap first gate in §6. The docs mention a `--strict` flag, but
  the installed Claude Code 2.1.143 does not offer one; `claude plugin tag`
  checks that `plugin.json` and the marketplace entry agree on the version.
- **The docs gate features on version numbers** (v2.1.2xx) that do not
  line up with the installed 2.1.143. Check every gate against the CLI
  actually in use when running §6.

### 3.2 Claude apps: Cowork, Desktop, claude.ai Team/Enterprise

Sources: [Cowork plugins](https://claude.com/docs/cowork/guide/plugins),
[plugins overview](https://claude.com/docs/plugins/overview),
[manage plugins for your organization](https://support.claude.com/en/articles/13837433-manage-plugins-for-your-organization),
[3P extensions](https://claude.com/docs/cowork/3p/extensions).

- **Same plugin format as Claude Code.** The docs state it directly, so no
  second Claude renderer is needed.
- **Personal install:** Customize → Plugins → upload a **`.zip`**. The limits
  are 200 MB uncompressed and 5,000 files.
- **Org-admin install:** Organization settings → Plugins & skills → upload a
  `.zip` under 50 MB. Plugin names are lowercase words joined by hyphens,
  ≤ 64 characters. **Re-uploading the same name overwrites the old version**,
  which is the "rebuild the shared plugin every other day" case
  `cor:agt:030:00` was ruled for.
- **Where the plugin sits inside the zip:** the 3P docs allow the plugin root
  at the zip root, or inside **one** wrapping folder. **UNCONFIRMED for the
  personal upload dialog.** The portal zips with the root at the zip root,
  which is the form every source accepts.
- **Skill frontmatter rules.** Sources:
  [platform skills overview](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview),
  [Agent Skills spec](https://agentskills.io/specification).
  - `name` is ≤ 64 characters of lowercase letters, digits and hyphens, and
    **must not contain `anthropic` or `claude`**.
  - `description` is ≤ 1024 characters.
  - The spec adds that `name` must match its directory. The server's renderer
    already enforces length. Whether it enforces the reserved words is
    unchecked (§6 row C5).
  - An older Help Center article gives a 200-character description limit.
    The platform docs and the spec both say 1024, so treat 200 as stale.

### 3.3 Codex (CLI 0.156.1 and the desktop app)

Sources: [build plugins](https://developers.openai.com/plugins/build/plugins),
[build skills](https://learn.chatgpt.com/docs/build-skills),
[plugin management](https://learn.chatgpt.com/docs/enterprise/plugin-management),
[Agent Plugins spec](https://agent-plugins.org/specification),
and `codex plugin --help`.

- **Codex has plugins, in its own native format.** Beyond the portal's
  finding:
  - The docs now prefer a portable **Agent Plugins 1.0** package: a root
    `plugin.json` with `$schema` and `name`, and skills discovered in
    `skills/`.
  - `.codex-plugin/plugin.json` remains a supported fallback.
  - Agent Plugins is **not** an Anthropic format. Its steering committee does
    not include Anthropic.
- **Codex reads the Claude layout, documented only for workspace (admin)
  import.** That import accepts `.claude-plugin/marketplace.json`, a
  standalone `.claude-plugin/plugin.json`, and "Claude-compatible plugins".
  For a **local CLI install** it is **UNCONFIRMED**. The binary's lookup order
  includes `.claude-plugin/plugin.json`, per the portal finding, but no doc
  states it.
- **Codex has no zip install.** Install is
  `codex plugin marketplace add <local dir | git>`, then
  `codex plugin add <plugin>@<marketplace>`.
- **A personal marketplace at `~/.agents/plugins/marketplace.json` is read
  automatically.** That is a **user-level** plugin location, relevant to §7
  Q1. Its entries use `source: {source: "local", path: "./plugins/x"}` plus a
  `policy` object. **Neither exists in Claude's marketplace schema**, so one
  marketplace file cannot serve both hosts natively.
- **Skills** are discovered from `.agents/skills` (from the working directory
  up), `~/.agents/skills` and `/etc/codex/skills`. `SKILL.md` requires both
  `name` and `description`. This is the path the portal's Codex download
  uses, and the one #621 writes to.

### 3.4 What this means for the producer

| | Claude (Code + apps) | Codex |
|---|---|---|
| One plugin layout serves… | Code, Cowork, Desktop and org upload, identically | its own layouts natively; Claude's only via documented admin import |
| Artifact a user can install without a terminal | the plugin **zip** (Cowork upload) | skill folders unzipped into `~/.agents/skills` (the portal's choice) |
| Artifact a terminal user installs lastingly | a directory with `marketplace.json` → `marketplace add` | a directory with `.agents/plugins/marketplace.json`, or `~/.agents/plugins` |
| Update signal | `version` in `plugin.json` | cache keyed `<marketplace>/<plugin>/<version>` (observed on disk) |

**The one safe common core** is `skills/<name>/SKILL.md`, with `name` equal
to the directory, ≤ 64 characters, and a description ≤ 1024 characters.
Everything around it (manifest file, marketplace file, archive) is per host.
That is the reason this proposal emits **one artifact per host** rather than
one hybrid, and the reason §7 Q8 asks whether to add Codex's native manifest.

## 4. What exists and can be reused

| Piece | Where | Reuse |
|---|---|---|
| Rendered `SKILL.md` per entry, with the server's action and reasons | `skillPlan(intent: EXPORT, host, files)` → `renderedBody`, `exportPlan{action, reasons}`, `findings` (`internal/api/queries/skills.graphql`, op `SkillExportPlan`) | **As is.** A bundle is built from nothing on disk, so the call passes no `files`. The portal already relies on this (§7 Q4). |
| Host table and limits | `skilldoc.Hosts` (`internal/skilldoc/skilldoc.go`) | As is: the host loop and the per-host name/description limits. |
| Per-host plan loop, run-to-the-end, `unrecognized` reported once | `internal/cmd/skill/export.go` (`newCmdExport`) | The loop shape and the "cannot start vs item failure" split (`api.MapError` → AuthRequired/Unavailable before any I/O is the run's error). |
| Report DTO vocabulary | `exportItemDTO`, `exportReasonDTO{code,message,origin}`, `exportUnrecognizedDTO` | Reuse the item/reason types so the two commands' `--json` read alike. The bundle report is its own top-level DTO (§5.3). |
| Skill-name grammar and path safety | `safeDirName`, skilldoc name validation | A name becomes a path inside the bundle; same refusal. |
| Link refusal on the output path | `checkRoot` / link guard in `export.go` | Same rule for the output directory: refuse writing through a symlink (Holger's Q2/Q3 ruling, #621). |
| Bundle layout rules | portal `src/lib/skill-bundle.ts` (`planBundle`, `pluginName`, `otherHostOnly`), portal#890 | **Mirror, don't import** (different language). Matching the portal's artifact byte-for-byte where the host allows it means a CLI-built and a portal-downloaded plugin are the same thing. Name and version are §7 Q6. |
| hadron-cli's own committed plugin | `.claude-plugin/marketplace.json`, `plugins/hadron-cli/` (hand-written `use-hadron-cli`) | Not generated by this command. The `use-hadron-cli` carve-out stands (plan §6). |

**What does not carry over from #621:** the disk walk, pairing, MOVE/REMOVE,
`--prune`, `--force`, hand-edit refusal. A bundle is written fresh into a
directory the command owns (§5.2), so there is no installed file to protect —
which is also why the plugin does **not** need #621's writer (the #653 issue's
early guess, confirmed by reading `export.go`).

## 5. Proposed shape

### 5.1 Command surface (proposed; contingent on §7 Q1 and Q2)

```
hadron skill export --plugin --out <dir> [--scope <name>] [--host <host>...] [--zip] [--dry-run] [--json]
```

or, equivalently, a sibling verb (`hadron skill bundle --out <dir> …`).
Which of the two is §7 Q2. Either way:

- **`--out <dir>` is required, as proposed.** Whether a user-level default
  replaces or joins it is §7 Q1. It never defaults to the current directory
  or a git toplevel (`cor:agt:030:02`: the destination never depends on the
  working directory or a checkout). The path is taken literally, with `~`
  expanded; a relative path is resolved against the working directory only
  because the user typed it.
- **Every known host by default** (`cor:agt:030:02`/`:03`: every target).
  `--host` narrowing is §7 Q3 — `:03` forbids narrowing *what is selected*
  by anything but a scope; whether picking *which artifact to build* is
  selection is not settled.
- **`--scope <name>`** narrows the selection (§5.4).
- **No `--force`, no `--prune`.** Nothing on disk is paired or protected.
- Never prompts.

### 5.2 Output: explicit, owned, no git

Per host, one artifact under `--out`:

```
<out>/<plugin-name>/            # Claude plugin root  (layout per §3)
<out>/<plugin-name>-codex/      # Codex artifact       (layout per §3)
```

The contents of each follow §3 and are settled by §7 Q8 (Codex) and Q10
(zip, directory, marketplace wrapper). A zip uses the portal's filenames,
`<plugin-name>.zip` and `<plugin-name>-codex.zip`.

- **The command owns the artifact directory, not `<out>`.** It creates
  `<out>` if missing and writes each host's artifact to a temp sibling, then
  renames it into place, so an interrupted run never leaves a half-plugin
  that installs. What happens when the artifact directory already exists
  (replace wholesale vs refuse without `--replace`) is §7 Q5; replacing is
  safe *only* if the directory carries this command's marker, mirroring
  #694's "never replace a file it did not write".
- **No git anywhere**: no toplevel lookup, no commit, no requirement that
  `<out>` be in or out of a repository. A user who wants the bundle in a repo
  (hadron-cli's maintainers) points `--out` there.
- **Symlinks on the output path are refused**, as in #621.

### 5.3 Report and exit

Same run-to-the-end semantics as #621 (`cor:agt:030:00`):

```json
{"dryRun": false,
 "hosts": [{"host": "claudeSkill", "artifact": "<out>/acme-skills", "format": "claude-plugin",
            "scanned": 12, "judged": 11,
            "included": [{"name": "...", "nodeId": "...", "urn": "..."}],
            "skipped":  [{"name": "...", "nodeId": "...", "urn": "...", "reasons": [{"code":"...","message":"...","origin":"server"}]}],
            "failed":   [...], "failure": null}],
 "unrecognized": []}
```

- `skipped` = the server withheld the body or planned a non-WRITE action
  (lint error, over-limit, collision, `disabled` is **not** listed — the
  portal's rule: a switched-off task is a choice, not an error).
- `failed` = client-side failures (unsafe name, duplicate name inside one
  bundle, write error).
- `failure` = the whole host could not be built (output path refused, plan
  refused); every entry is still named, as in #621's `blockHost`.
- **Other-host-only tasks** (declared for Claude only, so absent from the Codex
  plan) are reported on the host they are missing from, as the portal's
  `otherHostOnly` does — otherwise a Claude-only task silently vanishes from
  the Codex artifact.
- Exit 0 when every item was included; **5** after the full report when any
  item was skipped-with-error or failed; a run that cannot start exits with
  that error's code.

### 5.4 Selection

`skillPlan` takes `memories` (omitted = every memory the caller can read) and
no scope. `:03` allows narrowing **only by scope**. Two ways to honour that,
§7 Q7:

- **(a) client-side:** resolve `--scope` with `scopeExplain` (readable
  memories in scope order, `droppedCount` disclosed) and pass the result as
  `memories`. No server change; the CLI must report `droppedCount` so a
  scope that silently lost memories to access is visible.
- **(b) server-side:** a `scope` field on `SkillPlanInput`, resolved by the
  server. Matches the team rule that selection logic lives server-side so MCP
  and portal get it too (portal#890 currently passes an org's memories — the
  predefined per-org scope, done client-side).

No `-m` and no `--node` on the plugin producer, whichever is chosen.

**Stated, not discovered** (`:03`): an unnarrowed run stages every task the
caller can read, customer and personal included. The human report must say
so on a run with no `--scope` (one line, not a prompt).

## 6. Installation acceptance checklist

The PR that implements this is not done until each row is **observed**, on a
named host version, with the artifact built by the command. Record results in
the PR and in a finding node.

The corpus should include at least two tasks from **different** memories, one
task that is expected to be skipped (a lint error or over-limit
description), and one Claude-only declaration.

**Scratch HOME/profile only.** Never install into a teammate's or a
customer's real profile to test. A Cowork or org install uses a test org.

| # | Host / path | Steps | Pass when |
|---|---|---|---|
| C1 | Claude Code validator | `claude plugin validate <artifact>` (plugin) and `<out>` (marketplace) | exits 0, no warnings |
| C2 | Claude Code, one session | `claude --plugin-dir <artifact>` | `/plugin` lists it; each included skill is invocable as `/<plugin>:<skill>`; the skipped one is absent |
| C3 | Claude Code, lasting | `claude plugin marketplace add <out>` → `claude plugin install <plugin>@<marketplace>` (per §7 Q10's layout), **with `<out>` outside any git repo** | installs; survives restart; **no git needed** is the thing observed |
| C4 | Claude Code, update | change one task, re-run the producer, `claude plugin update` (or reinstall) | the changed body arrives, which **proves the §7 Q6 version rule works** |
| C5 | Frontmatter | inspect every emitted `SKILL.md` | `name` = directory, ≤ 64, no `anthropic`/`claude`; description ≤ 1024 |
| K1 | Cowork, personal | Customize → Plugins → upload `<plugin>.zip` | installs; a skill **triggers** on its description in a real conversation (not only listed). This also answers portal#879's trigger-phrasing concern |
| K2 | Cowork, zip root | repeat K1 with the plugin inside one wrapping folder | record accepted or refused. This settles §3.2's UNCONFIRMED claim either way |
| K3 | Org admin | upload the same zip in a test org; upload again after a change | installs; the second upload **overwrites**, per the doc |
| X1 | Codex, skills path | the Codex artifact placed in a scratch `~/.agents/skills` | `codex` lists and invokes each skill |
| X2 | Codex, plugin | `codex plugin marketplace add <codex artifact>` → `codex plugin add …` | installs, **if** §7 Q8 chose to emit a Codex plugin. Record which manifest Codex actually read |
| X3 | Codex reads Claude layout | `codex plugin marketplace add <claude artifact>` | record accepted or refused. This settles the local-install half of §3.3's UNCONFIRMED claim. Informational, not a gate |
| R1 | Report | the run's `--json` | every skipped or failed item named with its reason; other-host-only tasks named; `scanned`/`judged` per host; exit 5 iff an item failed |
| R2 | No git | the whole run with `<out>` in `/tmp` and the cwd outside any repo | identical result |

Rows **C2, C3, K1 and X1 are the gate.** Those are the claims #653 exists to
measure. The rest are required observations, recorded whichever way they
come out.

## 7. Unresolved decisions — listed, not chosen

Owner in brackets. **Q1 is a contract question**, so it goes to Vera and
Holger before implementation. The rest are implementation calls, Jane's
unless marked.

**Q1. Does an explicit `--out` fit `cor:agt:030:02`? [Vera → Holger]**
`:02` says an export goes to "one destination per host", user-level, and
"does not offer a choice among several". `:00` defines export as writing files
"where a host will find them". A bundle written to `--out` is none of those:
it is an artifact the user then installs. The options:
- **(a) A bundle is not an "export" under `:02`**, but an artifact whose
  install is the host's own act. `:02` would gain one sentence saying so.
  This is what plan §6/B8 and #653 assume.
- **(b) The producer writes to each host's user-level plugin location** that
  §3 found:
  - `~/.claude/skills/<plugin>/` (loads as `@skills-dir`);
  - `~/.agents/plugins/` plus the personal marketplace for Codex.
  That satisfies `:02` literally, but it does nothing for a Cowork user, who
  needs a zip to upload.
- **(c) Both**, as separate modes: (b) as the default, `--out` for an
  artifact meant for someone else.

Nothing here should be built until this is ruled, because it decides the
command's default behaviour.

**Q2. A flag on `skill export`, or a sibling verb? [Jane]**
- **`--plugin`:** one verb for "export". But `export`'s every other flag
  (`--force`, `--prune`, the disk walk) would then mean nothing in plugin
  mode, and would have to be refused there.
- **A sibling** (`skill bundle` / `skill plugin`): its flags stay honest.
  If Q1 lands on (a), the name should not say "export".

**Q3. Is `--host` narrowing a selection? [Vera]**
`:03`: "every exportable task … for every target", narrowed only by scope.
Building only the Claude artifact changes which *artifact* is produced, not
which *tasks* are selected. Whether `:03`'s "every target" forbids it is a
reading question. The portal already builds one host per download.

**Q4.** *(Resolved while writing.)* Does a no-files EXPORT plan give the right
actions? Yes: portal#890 calls `skillPlan(intent: EXPORT, host, memories)`
with no `files`, and keeps an entry only when it has a `renderedBody` and a
`WRITE` action.

**Q5. An artifact directory that already exists. [Jane]**
- Replace it wholesale when it carries this command's marker (as #694 does
  for a single file).
- Or refuse unless `--replace` is given.
Either way, never merge into it. A stale skill left behind is the
orphan-and-duplicate problem of plan §11a, recreated.

**Q6. Plugin `name` and `version`. [Jane; name possibly Holger]**
- **Name:** the portal uses `<org>-skills` (`pluginName`). A CLI run spans
  every org the caller can read, so there is no single org. The options are:
  - derive it from `--scope` when one is given, else `hadron-skills`;
  - a required `--name`;
  - the portal's rule applied when the scope *is* an org scope.
  The name is user-visible (`/<plugin>:<skill>`) and permanent per install,
  so it deserves a deliberate call.
- **Version:** it must change whenever the content changes, or Claude users
  never receive the update (§3.1). The options are:
  - a content-hash pre-release (`0.0.0-<hash8>`);
  - a timestamp;
  - a user-supplied `--version`.
  The portal sets **no** version today. Check whether that is right for
  Cowork re-uploads (K3), and align the two surfaces either way.

**Q7. Scope resolution: client or server. [Ada → Dara/Eli if server]**
§5.4 (a) vs (b). Option (b) is a server change and follows the team's
server-side-logic rule. Option (a) ships without waiting. If (a), it is
temporary and should be named so.

**Q8. The Codex artifact. [Jane, after X1–X3]**
- The portal's skill folders for `~/.agents/skills` (documented, and the
  only no-terminal path).
- A Codex plugin with a native manifest: `.codex-plugin/plugin.json`, or an
  Agent Plugins root `plugin.json`.
- Both.
Measure X2/X3 before choosing. Do not ship a Codex plugin on the strength
of the UNCONFIRMED Claude-layout fallback.

**Q9. The collision with #621 in `~/.claude/skills`. [Jane, only if Q1 ≠ (a)]**
If the producer writes a plugin folder into the root that #621's per-skill
export also writes, a user who does both gets every skill twice: bare, and as
`<plugin>:<skill>`. #621's walk would also see a plugin directory it did not
write, and report it as foreign. Decide the interaction before shipping (b)
or (c).

**Q10. Directory, zip, or both by default; and the marketplace wrapper. [Jane]**
- Cowork needs a zip; a terminal install needs a directory with
  `marketplace.json`.
- The candidate default is both, with the zip holding the plugin root at its
  root and the directory wrapped as a one-plugin marketplace.
- Settle whether the marketplace wrapper belongs in the zip. Cowork ignores
  it; `--plugin-url` may not.

**Q11. Warning findings. [Jane with Eli]**
The portal reports only error findings. `skill-description-no-trigger` is a
warning, and a Cowork skill without trigger phrasing reportedly never fires
(portal#879). Decide whether the human report lists warnings; `--json`
should carry them regardless.

## 8. Handoff to Jane

**Order.**
1. Get Q1 ruled (Ada routes it to Vera and Holger). Everything in §5.1–5.2
   depends on it.
2. Settle Q2, Q6 and Q10 in the implementing PR's description.
3. Build.
4. Run §6.
The build can start on the parts every Q1 answer shares:
- the per-host plan loop (`files: []`);
- the included/skipped/failed split, mirroring `planBundle` including the
  duplicate-name and unsafe-name refusals;
- the report DTO;
- rendering the Claude plugin tree in memory.
**Writing it anywhere** is the part Q1 decides.

**Tests.** The #621 pattern applies:
- a fake `SkillExportPlan` per host through `captureGraphQL`;
- assert `host` on each call, and `files` absent or empty;
- assert `memories` equals the scope's resolved list (Q7a) and is omitted
  when there is no scope;
- golden trees for each artifact;
- the link refusal on `--out`;
- a partial-write test that proves an interrupted run leaves no half-plugin.
**No `t.Skip`.**

**Do not reuse** #621's MOVE/REMOVE, pairing or `--prune` code. §4 explains
why none of it applies.

**Docs.**
- Add the command to `agentic-usage.md`, with its exit codes.
- The how-to is Tove's. Report the as-built shape to Ada for routing, per
  the team rule.
- Mark `skill-command-group.md` §6's "the producer's surface is not defined
  here" as answered by this doc once Q1 is ruled.

**Spec question.** If Q1 is ruled (a) or (c), `cor:agt:030:02` needs its
sentence. That is Vera's to mint, with Holger's confirmation. Don't mint a
citation from the CLI side.

**Open issues touching this.**
- portal#879 is closed and portal#890 is merged. Keep the artifacts in
  parity, and report divergence to Ada rather than changing the portal.
- server#1309 (legacy pairing) does **not** affect this. A bundle pairs
  nothing.
