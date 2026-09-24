# Design proposal: `hadron` plugin export — an installable multi-skill bundle (#653)

> **Status: BUILT as `hadron skill plugin` (#653, Jane, 2026-09-24).** §9 is
> the as-built record: every §7 call that was Jane's is decided there, with the
> acceptance rows actually observed. §1–§8 are Jonas's design (team chat
> #1365, cli#700/#702/#703), kept as written so the decisions can be read
> against the options they chose between.

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
unit a user installs (`cor:agt:030:03`). Plan `skill-command-group.md` §6
expects hadron-cli's own committed plugin to be built by one invocation of
it. That plugin also carries a hand-written skill, which this producer does
not generate and must not delete, so how the two meet is §7 Q12, not
settled here.

## 2. Contracts this rests on

| Contract | What it fixes for this command |
|---|---|
| [`cor:agt:030:00`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:00) | Node is authoritative, file derived, nothing flows back. **Run to the end:** an item is one skill for one host; one failing never stops the others; the end-of-run report names every failure and every declaration nothing could export. **No prompting** mid-run. |
| [`cor:agt:030:01`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:01) | The rendered file is derived from the node; provenance header. |
| [`cor:agt:030:02`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:02) | User-level destinations, one per host, every known host unconditionally; **no repo-level destination; never dependent on the working directory or a checkout.** These govern **individual skill export** (#621). The plugin is a **separate producer** with an explicit output directory, and `:02` needs no change for it (Holger, B8, team chat #1108; §7 Q1). |
| [`cor:agt:030:03`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:03) | A plugin is one installable unit carrying many skills. **Selection:** every enabled task the caller can read, for every target; no visibility filter; **narrowed only by a scope** (never by memory picks or named nodes). **Format, layout and manifest are deliberately outside the contract** — so §3 of this document is implementation and may change without superseding anything. |
| [`cor:agt:030:05`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:05) | A skill over its host's limit is out of export (an item failure, reported). |
| [`cor:agt:030:06`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:agt:030:06) | Only an enabled declaration exports; name collisions are item failures. |

Rulings already made and not reopened here *(ruled)*:

- **Server-rendered bodies and judgments** — the client renders nothing and
  invents no class or action (Ada on #653, 2026-09-24; same seam as #621).
- **Canonical hosts** `claudeSkill` / `codexSkill`; `SkillPlanInput.host` is
  singular, so two hosts are two plans with separately reported
  `scanned`/`judged` (Dara, measured on `8bbae1d`).
- **The plugin is its own producer, writing to an explicit output directory
  with no git requirement.** Individual export stays user-level and all-host,
  and `cor:agt:030:02` needs no change (Holger, B8, team chat #1108, relayed
  by Eli, 2026-09-23; retained in Ada's #653 comment of 2026-09-24).
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
  - This is a **user-level location with no marketplace**. It is noted for
    completeness only: under the B8 ruling the producer writes to `--out`,
    not here (§7 Q1).
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
  automatically.** That is a **user-level** plugin location, noted for
  completeness; the producer does not write there (§7 Q1). Its entries use `source: {source: "local", path: "./plugins/x"}` plus a
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
| Link refusal | `checkRoot` / link guard in `export.go` | **The rule, not the helper.** `checkRoot` only checks below `$HOME`, while `--out` can be anywhere. §5.2 moves the same split (follow the user-named anchor, refuse links below it) to `--out`. |
| Bundle layout rules | portal `src/lib/skill-bundle.ts` (`planBundle`, `pluginName`, `otherHostOnly`), portal#890 | **Mirror, don't import** (different language). Matching the portal's artifact byte-for-byte where the host allows it means a CLI-built and a portal-downloaded plugin are the same thing. Name and version are §7 Q6. |
| hadron-cli's own committed plugin | `.claude-plugin/marketplace.json`, `plugins/hadron-cli/` (hand-written `use-hadron-cli`) | `use-hadron-cli` is not generated by this command, and the carve-out stands (plan §6). Because §5.2 writes fresh and Q5 forbids merging, an invocation that builds **this** plugin (one whose artifact is named `hadron-cli`, the name the committed plugin and its marketplace entry already use) **would delete it wherever Q5 allows the replacement**: always under "replace a marked artifact", or with `--replace` under "refuse unless" (@codex on #700). Without an authorized replacement it is refused, and nothing is deleted. With any other name (Q6's candidates), the artifact lands beside `plugins/hadron-cli`, not over it (@copilot on #702). §7 Q12 lists the ways out. |

**What does not carry over from #621:** the disk walk, pairing, MOVE/REMOVE,
`--prune`, `--force`, hand-edit refusal. A bundle is written fresh into a
directory the command owns (§5.2), so there is no installed file to protect —
which is also why the plugin does **not** need #621's writer (the #653 issue's
early guess, confirmed by reading `export.go`).

## 5. Proposed shape

### 5.1 Command surface (proposed; the verb is §7 Q2)

```
hadron skill export --plugin --out <dir> [--scope <name>] [--host <host>...] [--zip] [--dry-run] [--json]
```

or, equivalently, a sibling verb (`hadron skill bundle --out <dir> …`).
Which of the two is §7 Q2. Either way:

- **`--out <dir>` is required** (B8, §2). It never defaults to the current
  directory or a git toplevel (`cor:agt:030:02`: the destination never
  depends on the working directory or a checkout). The path is taken
  literally, with `~` expanded; a relative path is resolved against the
  working directory only because the user typed it.
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
- **Links on the output path.** #621's `checkRoot` cannot be reused as it
  stands: it `lstat`s only the components *below* the resolved `$HOME`, and
  `--out` may be anywhere (@copilot on #700). A generic rule of "refuse any
  link on the absolute path" would refuse `/tmp` on macOS, which is itself a
  link to `/private/tmp`. The proposal applies #621's split to a new anchor:
  - **`--out` itself is resolved** with `EvalSymlinks`, as `$HOME` is. The
    user named it, and following the link they typed is what they asked for.
    The report shows the **resolved** path. If `--out` does not exist yet,
    its nearest existing ancestor is resolved and the rest is created.
  - **Everything the command creates or replaces below it is `lstat`ed and
    refused if it is a link**: the artifact directory, every directory inside
    it, and every file it writes. This is the part that matters. Replacing an
    artifact directory that is a link would delete through the link (§7 Q5).
  - An artifact written to a temp sibling and renamed into place never
    follows a link at the destination, because the rename replaces the name.

### 5.3 Report and exit

The same run-to-the-end semantics as #621 (`cor:agt:030:00`), and **the same
item type**. Every row is #621's `exportItemDTO`, unchanged:
`{node, nodeId, name, reasons, kept}`. `kept` is always `[]` here, since a
bundle keeps nothing on disk. Each item is bucketed by the **action the
server planned**, as #621 does, and never re-judged by the client:

```json
{"dryRun": false,
 "scope": {"id": "...", "name": "...", "memoryCount": 4, "droppedCount": 1},
 "hosts": [{"host": "claudeSkill", "artifact": "/Users/me/out/acme-skills", "format": "claude-plugin",
            "failure": null, "scanned": 12, "judged": 11,
            "included": [{"node": "hrn:node:…", "nodeId": "…", "name": "…", "reasons": [], "kept": []}],
            "skipped":  [{"node": "hrn:node:…", "nodeId": "…", "name": "…",
                          "reasons": [{"code": "…", "message": "…", "origin": "server"}], "kept": []}],
            "refused":  [], "failed": [], "notForHost": [],
            "findings": [{"node": "hrn:node:…", "nodeId": "…", "name": "…",
                          "rule": "skill-description-no-trigger", "severity": "warning", "message": "…"}]}],
 "unrecognized": []}
```

- **`included`**: the server planned `WRITE` and sent a `renderedBody`.
- **`skipped`**: the server planned `SKIP`. This includes **`disabled`
  declarations**, which are listed with the server's reason (@copilot on
  #700). `cor:agt:030:00`'s report names what was not exported, and #621
  reports the same class. A skip is not an error. The portal leaves disabled
  tasks out of its download report; that is a UI choice, and the `--json`
  contract should not copy it.
- **`refused` / `failed`**: the server planned `REFUSE` / `FAIL` (a lint
  error, over-limit, collision). Client-side failures are added to `failed`
  with `origin: client`: an unsafe name, a duplicate name inside one bundle,
  a `WRITE` that arrived without a body, a write error, or a `MOVE`/`REMOVE`,
  which a no-files plan should never produce and is reported rather than
  silently dropped.
- **`failure`**: the whole host could not be built (output path refused, plan
  refused). Every entry is still named, as in #621's `blockHost`.
- **`findings`**: every server finding for every judged entry, **except an
  `error` on an entry already in `refused` or `failed`**, since that one is
  carried by the entry's reasons. There is one row per finding, keyed by node
  like the item rows (@codex on #700).
  - They sit beside the item lists rather than inside them, so
    `exportItemDTO` stays exactly #621's.
  - An error finding is left out **only** when its entry is `refused` or
    `failed`. An error on an entry the server planned otherwise stays here.
    The case that matters is a **disabled** declaration: discovery still
    judges it, and the server plans it `SKIP`. Dropping that error would
    lose it from the report (@copilot on #702).
  - The client **surfaces** such an error and does **not charge** it. The
    entry stays `skipped`, and the exit status follows the server's action
    (the rule below). Promoting it to a failure would re-judge the server's
    plan, which §5.3 rules out. If a disabled node's error should fail the
    run, that is a server planning change, not a client rule.
  - **Today the values are `error` and `warning`.** `SkillFinding.severity`
    is documented as "Either error or warning". But it is a `String!`, not an enum, so the
    server could add a value without a schema change (@copilot on #702). An
    unknown severity is therefore **passed through verbatim** into this list,
    never dropped and never promoted to an error. Only `error` is
    interpreted.
  - **This list never affects the exit status.** Error findings do, but only
    through their entry's `refused`/`failed` bucket (the exit rule below).
  - Whether the **human** report lists them is still Q11. #621's `skill export` has no such field either; adding
  one there is a separate, additive change.
- **`notForHost`**: tasks declared only for the *other* host, so absent from
  this host's plan. These are reported as the portal's `otherHostOnly` does;
  otherwise a Claude-only task silently vanishes from the Codex artifact.
  It is informational and not an error.
  - **Where the names come from:** the other host's plan, not this one's
    counts. A run that builds both hosts already holds both plans, so host
    X's `notForHost` is the other plan's entries that X's plan did not judge,
    minus `disabled` ones. That is `otherHostOnly`, and it needs no extra read.
  - A run narrowed to one host (§7 Q3) must still fetch the other host's
    plan to fill this, as the portal does with a `STATUS` plan
    (`routes/app/orgs/[id]/tasks/plugin/+server.ts`). Otherwise
    `notForHost` would be empty for the wrong reason (@copilot on #702).
- **`scope`** is `null` when no `--scope` was given. When one was, it carries
  `droppedCount` (scope memories the caller cannot read), so a JSON-only
  caller gets the same disclosure as the human report (@copilot on #700).
  The field is needed under either §7 Q7 option.
- **Exit status**, identical to #621's `exportHasFailures`: **5** after the
  full report when **any host has a `failure`, or any item is `refused` or
  `failed`**. Otherwise 0, including when items were only `skipped`. A run
  that cannot start exits with that error's code, and so does an empty scope
  (§5.4).

### 5.4 Selection

`skillPlan` takes `memories` (omitted = every memory the caller can read) and
no scope. `:03` allows narrowing **only by scope**. Two ways to honour that,
§7 Q7:

- **(a) client-side:** resolve `--scope` with `scopeExplain` (readable
  memories in scope order, with `droppedCount` disclosed) and pass the result
  as `memories`. No server change is needed. The report carries
  `droppedCount` (§5.3).
- **(b) server-side:** a `scope` field on `SkillPlanInput`, resolved by the
  server. Matches the team rule that selection logic lives server-side so MCP
  and portal get it too (portal#890 currently passes an org's memories — the
  predefined per-org scope, done client-side).

No `-m` and no `--node` on the plugin producer, whichever is chosen.

**An empty scope must never widen (@codex P1 and @copilot on #700).**
`SkillPlanInput.memories` carries `omitempty`, and the server reads an
omitted `memories` as *every memory the caller can read*. So under (a), a
scope that resolves to **zero** readable memories (an empty scope, or one
whose memories were all dropped by access) would, passed through naively,
export **everything**: the exact opposite of the narrowing asked for,
including customer and personal tasks.
- The producer must check for zero before planning: an empty resolved scope
  **never calls `skillPlan`**. It reports the scope, its `droppedCount` and
  "nothing to export", writes no artifact, and exits with a usage-class error.
  An empty artifact would read as success.
- The portal already has this guard ("NEVER call skillPlan with an empty
  list", `routes/app/orgs/[id]/tasks/plugin/+server.ts`).
- (b) removes the hazard at the source, which is another argument for it in
  §7 Q7.

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
| R1 | Report | the run's `--json` | every skipped, refused and failed item named with its reason, disabled declarations included; `notForHost` named; `scanned`/`judged` per host; `scope.droppedCount` present when a scope was given; a planted warning on an otherwise valid entry appears in `findings`, is **not** repeated in that entry's `reasons`, and the entry itself stays in its action bucket (`included`, for a `WRITE`); **exit 5 iff a host `failure`, or any `refused` or `failed` item** (§5.3) |
| R3 | Empty scope | `--scope` naming a scope with no readable memories | no `skillPlan` call at all, no artifact, a non-zero exit, and the report says the scope was empty |
| R2 | No git | the whole run with `<out>` in `/tmp` and the cwd outside any repo | identical result |

Rows **C2, C3, K1 and X1 are the gate.** Those are the claims #653 exists to
measure. The rest are required observations, recorded whichever way they
come out.

## 7. Unresolved decisions — listed, not chosen

Owner in brackets. Every open question here is an implementation call,
Jane's unless marked. None needs a new contract.

**Q1.** *(Resolved: already ruled; listed so the numbering holds.)* Does an
explicit `--out` fit `cor:agt:030:02`? The first version of this plan asked
it anew. **Holger ruled it on 2026-09-23 (B8, team chat #1108):** individual
export writes user-level for every known host; **the plugin is its own
producer, writing to an explicit output directory with no git requirement**;
and `:02` needs no change. Ada pointed out the re-ask (#1418).

**The only thing found since the ruling is a fact, not a conflict.** Both
hosts turn out to have a user-level *plugin* location (§3.1, §3.3). The
ruling doesn't depend on that, and nothing here proposes writing there.
Writing there in addition to `--out` would be a new feature request against
a settled ruling, and it would bring Q9's collision with it. It is not an
open question for this build.

**Q2. A flag on `skill export`, or a sibling verb? [Jane]**
- **`--plugin`:** one verb for "export". But `export`'s every other flag
  (`--force`, `--prune`, the disk walk) would then mean nothing in plugin
  mode, and would have to be refused there.
- **A sibling** (`skill bundle` / `skill plugin`): its flags stay honest.
  Under B8 the plugin is a separate producer, not a mode of export,
  which argues for the sibling. The name shouldn't say "export".

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

**Q9. An `--out` at or inside a host's skills root. [Jane]**
B8 makes the destination explicit, but it doesn't stop a user from choosing
a host root, for example `--out ~/.claude/skills` (@codex on #703).
- The producer would then write `~/.claude/skills/<plugin-name>/`, which
  Claude Code loads as a `@skills-dir` plugin (§3.1).
- A user who also runs #621's export gets every skill twice: bare, and as
  `<plugin>:<skill>`.
- #621's walk would see a plugin directory it didn't write and report it as
  foreign.

The options:
- **refuse** when any **resolved artifact path** (`<out>/<plugin-name>`,
  `<out>/<plugin-name>-codex`, and any zip) is, or is inside, any host's
  skills root. The roots are #621's `exportRoots` table.
  - Checking `--out` alone is not enough: `--out ~/.claude` with a plugin
    name of `skills` would pass that check and still write exactly
    `~/.claude/skills` (@codex on #703).
  - The check runs after resolving, on the same paths the writer will use,
    and **before anything is written**;
- or allow it with a warning in the report.

Refusing keeps both commands' territory disjoint, and matches #621 refusing
what it didn't write. It is the proposal, and Jane's call.

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
(portal#879). Decide whether the human report lists warnings. `--json`
carries them regardless, in the per-host `findings` list (§5.3).

**Q12. hadron-cli's committed plugin and its hand-written skill. [Jane; Holger if (c)]**
`plugins/hadron-cli/` holds `use-hadron-cli`, which is hand-written on
purpose (plan §6). A fresh, never-merged artifact (§5.2, Q5) cannot also
keep it (@codex on #700). The options:
- **(a) Keep the committed plugin out of this producer.** It stays
  hand-maintained, and generated task skills, if the repo ships them at all,
  go in a **second** plugin in the same marketplace. Plan §6's planned drift
  gate must then be **re-pointed** at that second plugin's skills directory,
  with `status --to <dir>`, because `--to plugin` is fixed to
  `plugins/hadron-cli/skills`. Otherwise every generated task reads as
  `never-exported` there and `--strict` fails on every run. The same
  failure applies to (c) (@codex and @copilot on #702).
- **(b) A `--seed <dir>`** copied into the artifact before the generated
  skills. A generated name that collides with a seeded one is refused as an
  item failure, never an overwrite.
  - **The seed is a separate tree holding only hand-written content**, for
    example `plugins-src/hadron-cli/`. It is never the previous artifact
    (@codex on #702). Seeding from `plugins/hadron-cli/` would copy the last
    build's generated skills forward, so a removed or renamed declaration
    would stay installable: the stale-file problem §5.2 and Q5 exist to
    prevent.
  - The producer **refuses a seed that overlaps the output**: the same
    directory, or either one inside the other (compared after resolving).
  - **Links in the seed are refused**, not only at its root. An `lstat` walk
    of the whole seed allows directories and regular files and rejects
    everything else (symlinks, FIFOs, devices, sockets) before anything is
    copied. A copy that followed a link could package files from
    outside the seed or loop, and one that preserved it would break §5.2's
    link-free artifact (@codex on #702).
  - **Producer-owned paths are reserved.** Every manifest the producer
    writes belongs to it: `.claude-plugin/plugin.json`, and for a Codex plugin
    whichever of `.codex-plugin/plugin.json` or a root `plugin.json` Q8
    selects (@codex on #702). So does any marketplace file. A seed containing one is
    refused, never merged or overwritten in either direction. A seeded
    `skills/<name>/` that a generated skill also claims stays the
    **item-level** failure above: the generated item fails, and the seed and
    every other item carry on. Otherwise a seeded manifest's fixed `version` would
    silently defeat C4's update rule (@codex on #702). The committed
    manifest's hand-written metadata (today `description` and `author` in
    `plugin.json`, plus the marketplace file's `owner` and `metadata`) would
    therefore need a home in Q6's naming/metadata decision (@copilot on
    #702).
  - **Report:** each host gets a `seeded` list, initialized to `[]` and
    empty unless `--seed` is given. It has **one row per seeded file**, with
    its path relative to the artifact root
    (`{"path": "skills/use-hadron-cli/SKILL.md"}`). Directories are implied
    by their files, so granularity and collision checks work on one unit
    (@copilot on #702). This makes the one
    non-server-rendered part of the artifact visible to a JSON consumer. It
    has no node, no action and no effect on the exit status. R1 would then
    also check `seeded`.
- **(c) Ship no generated skills in the repo's plugin.** Plan §6's drift gate
  must then be **retired**, not merely called moot. It runs
  `skill status … --to plugin --strict` against the committed plugin, and
  with no generated files every selected declaration would be
  `never-exported` drift, an error under `--strict`, so the gate would fail
  on every run (@copilot on #702). The gate is planned but not built (no
  `skill-drift` workflow exists in `.github/workflows/`), so retiring it
  means striking it from plan §6. Reversing plan §6 is Holger's call.

**Only (b) leaves plan §6's gate as written.** (a) re-points it and (c)
retires it. Either way, the choice amends plan §6 as well as this doc.

Until one is chosen, **no invocation that builds an artifact named `hadron-cli` into `plugins/`**. The §8 build does
not depend on this: it concerns one invocation, not the command.

## 8. Handoff to Jane

**Order.**
1. Settle Q2, Q6 and Q10 in the implementing PR's description.
2. Build. Nothing in §5 waits on a ruling. The destination is `--out` (B8).
   Q12 blocks only one **invocation** (building the committed `hadron-cli`
   plugin), not the producer.
3. Run §6.

**Tests.** The #621 pattern applies:
- a fake `SkillExportPlan` per host through `captureGraphQL`;
- **an empty resolved scope issues no `SkillExportPlan` request at all.**
  Assert the operation is absent from the captured requests, not only that
  `memories` is empty (the `assert-the-query-not-the-capture` review node);
- links below `--out` refused, and `--out` itself followed and reported
  resolved (§5.2);
- assert `host` on each call, and `files` absent or empty;
- assert `memories` equals the scope's resolved list (Q7a) and is omitted
  when there is no scope;
- golden trees for each artifact;
- a partial-write test that proves an interrupted run leaves no half-plugin.
**No `t.Skip`.**

**Do not reuse** #621's MOVE/REMOVE, pairing or `--prune` code. §4 explains
why none of it applies.

**Docs.**
- Add the command to `agentic-usage.md`, with its exit codes.
- The how-to is Tove's. Report the as-built shape to Ada for routing, per
  the team rule.
- Mark `skill-command-group.md` §6's "the producer's surface is not defined
  here" as answered by this doc.

**Spec question: none.** B8 ruled that `cor:agt:030:02` needs no change for
the plugin. Don't mint or amend a citation from the CLI side.

**Open issues touching this.**
- portal#879 is closed and portal#890 is merged. Keep the artifacts in
  parity, and report divergence to Ada rather than changing the portal.
- server#1309 (legacy pairing) does **not** affect this. A bundle pairs
  nothing.

## 9. As built (Jane, 2026-09-24)

**Command:** `hadron skill plugin --out <dir> [--name <name>] [--scope <name|id>] [--zip] [--dry-run] [--json]`
(`internal/cmd/skill/plugin.go`). §5's report shape and exit rule are built as
written, including every `@copilot`/`@codex` refinement in §5.3 and §5.4.

### 9.1 Decisions

| Q | Decided | Why |
|---|---|---|
| Q2 | **A sibling verb, `skill plugin`.** | B8 makes the plugin a separate producer; `export`'s `--force`/`--prune`/disk walk mean nothing here, and as a flag they would have to be refused. |
| Q3 | **No `--host` in v1.** Every host is always built. | `:03`'s "every target" with no reading question. Additive later if Vera rules it is not selection. |
| Q5 | **Replace wholesale only when marked; otherwise the host fails (`artifact-not-ours`). No `--replace`.** | The marker is `.hadron-plugin` in the directory, and the zip's comment for a zip. A link at either path fails as `artifact-is-link`. **The guarantee holds at the instant of every move** (review rounds 1–3 on #707). A previous artifact is re-verified after it is moved aside. Everything is put onto a user-visible name only by `publish()`: a hard link for a file (a filesystem without hard links is refused under `--zip`), and a rename for a directory, which cannot displace a file or a non-empty directory. A put-back that finds the name occupied leaves the old artifact at its moved-aside name and says where. **Stated residue:** an EMPTY directory can be displaced (nothing to lose), and the moved-aside name is a fresh random one no user path points at. |
| Q6 name | **`hadron` by default, `--name` overrides** (lowercase words joined by hyphens, ≤ 64). | **Holger, 2026-09-24:** a plugin can carry more than skills, so not `hadron-skills`. Diverges from the portal's `<org>-skills`, which was flagged to Ada for Gil (team chat #1470). |
| Q6 version | **`0.0.0-h<12 hex>`, a hash of everything the version stands for:** the plugin name, the manifest description (which names the scope) and every skill's name and body, each part length-prefixed. | Unchanged content keeps its version; any change moves it. **Measured:** Claude Code 2.1.143 compares versions for EQUALITY. A lower-sorting `0.0.0-0aa111` still updated from `0.0.0-abc123`, and the new body arrived (§6 C4). The `h` keeps the identifier alphanumeric, since an all-digit one with a leading zero is invalid semver. |
| Q7 | **(a), client-side, named temporary in the code.** | `scopeExplain` resolves the readable memories in order. An empty result never calls `skillPlan` and exits 2. |
| Q8 | **Codex: skill folders only** (the portal's shape). | No native Codex manifest until X2/X3 are measured. |
| Q9 | **Refuse, before any request (exit 2).** The check is on each ARTIFACT path, not `--out`. | Refused when the path is at or inside a host skills root: by path shape (`.claude/skills`, `.agents/skills`, `.codex/skills`, so project-level roots count, with no git), as typed and as resolved, and against the user-level roots resolved on disk. Also refused when it is ABOVE a resolved one (team chat #1436). |
| Q10 | **A directory by default; `--zip` adds zips beside it.** The Claude directory is **also a one-plugin marketplace** (`.claude-plugin/marketplace.json`, `source: "./"`). | **Measured:** it validates, installs through `claude plugin marketplace add <dir>` + `install <name>@<name>`, and updates. The zip carries only `plugin.json` + `skills/`, the portal's layout. |
| Q11 | **The human report lists every finding** (severity, rule, message). | `--json` carries them in `findings` regardless. |
| Q12 | **(a)** (team chat #1436). | The committed `hadron-cli` plugin stays hand-maintained. This command's default name (`hadron`) no longer collides with it. |

### 9.2 Acceptance observed

These were run on the command's own output against production (`skill plugin
--out <tmp> --zip`: 20 included, 10 skipped with the server's reasons, 13
findings). The install rows used a scratch `HOME`/`CLAUDE_CONFIG_DIR`, with
`<out>` outside any git repo.

| Row | Result |
|---|---|
| C1 | **Pass.** `claude plugin validate <artifact>` is clean. |
| C3 | **Pass.** `marketplace add` + `install hadron@hadron` installs all 20 skills, version `0.0.0-h9fafc9d1129f`, with no git. |
| C4 | **Pass** (on a synthetic bundle with the identical layout): a changed body plus a moved version arrived via `claude plugin update`. |
| C5 | **One finding, for the server.** `hadron-export-task-as-claude-skill` contains the reserved word `claude`. The server's renderer does not enforce it (the open question in §3.2), so it was reported to Ada for routing. Every `name` equals its directory and is ≤ 64. |
| R1/R2/R3 | Pinned by `internal/cmd/skill_plugin_test.go` and `internal/cmd/skill/plugin_test.go`. |
| C2, K1–K3, X1–X3 | **Not run.** C2 and X1 need a model session; K1–K3 need a person at the Claude app and a test org. They are handed over, not claimed. |

