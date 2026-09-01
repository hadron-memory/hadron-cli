# `agent create --install-into` — design as built

Addresses §1 of #535. Ships in PR #543.

## Problem

A newly created agent is in **no App's cast pool**, so `team worker cast` cannot
resolve it. Nothing said so: `✓ created` reads as done, and the failure surfaced
later, at cast time, as `WORKER_AGENT_NOT_FOUND` — which points at the *role*
rather than at the missing install. Standing up one role meant three commands
across two groups, with the middle one discoverable only by failing.

## What was built

`agent create --install-into <app>` performs `installAgentIntoApp` against the
agent it just created, in the same run. Without the flag, the create emits a
stderr note naming both remedies.

## Decisions

### 1. Not spelled `--app`

#535 suggests `--app`. Rejected: `--app` is the **persistent App-CONTEXT flag**,
and it never acts on an App. `agent list --app` prints a note saying exactly
that (`cmdutil.NoteAppIsContextOnly`, #383) — and that note's only caller is in
this same command group. Giving `create --app` a real side effect next to
`list --app` being inert would put two meanings on one flag inside one group and
institutionalize the confusion #383 exists to correct.

Alternatives considered and declined (Holger's call):

- **`--app` plus make `agent list --app` actually filter.** Makes the group
  consistent, but is a behaviour change to an existing command and belongs in
  its own PR.
- **Honour the persistent `--app`/config default as the install target.** Worst
  option: an ambient configured App would silently install every agent you
  create.

A local `--install-into` also avoids shadowing the persistent flag, so the
config default can never leak in as an install target.

### 2. Non-atomic, and never rolled back

Nothing server-side spans the two mutations. The install can fail *after* the
create succeeded — and this is not exotic: installing needs **CONTRIBUTOR+ on
the App's owning org**, a stricter gate than creating the agent, so `FORBIDDEN`
is the expected failure.

Rules:

- The agent is **always emitted** (human output and `--json`), even when the
  install fails. Losing a just-created agent's URN is the one outcome with no
  cheap recovery.
- The agent is **never deleted** to "undo" the create. Deleting an agent the
  user asked to create, because a second step failed, is the more destructive
  repair.
- The error states `CREATED but NOT installed`, tells the caller not to re-run
  `agent create` (which would make a second agent), and prints the exact
  `app agent add` that finishes the job.

### 3. A lost answer is `unknown`, not `failed`

Raised by Codex on PR #543. `exitcode.Unavailable` (#394) is documented as *the
only class after which a mutation's outcome is genuinely unknown*. Reporting it
as `failed` and directing the caller to `app agent add` would invite a
`Conflict` (exit 5, "duplicate install") on an install that actually committed.

So `installStatusFor` maps `Unavailable` to status `unknown`, and the error says
the outcome is unknown and directs the caller to **look before acting**
(`app agent list`) rather than to retry blind. The exit code stays 7, so a
script branching on 1-vs-7 keeps working.

### 4. `--json` contract

`install` is present **only** when `--install-into` is passed, so a plain create
emits byte-identical JSON to before — the contract is extended, never changed.
The DTO embeds the existing `agentDTO`, so agent fields stay top-level.

```jsonc
// success
{ "id": "agt1", "urn": "…", /* … */
  "install": { "appRef": "hrn:app:acme.com:eng-team", "appId": "app1",
               "appUrn": "hrn:app:acme.com:eng-team", "status": "installed" } }

// refusal
{ "id": "agt1", /* … */
  "install": { "appRef": "app1", "status": "failed", "error": "…" } }

// lost answer
{ "id": "agt1", /* … */
  "install": { "appRef": "app1", "status": "unknown", "error": "…" } }
```

`appRef` echoes what the caller passed; `appId`/`appUrn` are the **server's
resolved** values and are `omitempty`, so they appear only on success. Raised by
Copilot on #543: `--install-into` accepts an ID *or* a URN, so populating
`appUrn` from the raw ref would let that field carry a non-URN.

### 5. FORBIDDEN guidance moved to `cmdutil`

`forbiddenGuidance` was unexported in the `app` command package. Two command
groups now reach the same mutation, and a second copy of that prose would drift
from the first the next time the rule moves — so it is
`cmdutil.InstallForbiddenGuidance` and both call it.

`cmdutil` rather than exporting it from `app`: `agent`→`app` would be a
command-package dependency that could cycle later, since the #405
`ParseVisibility` precedent already runs `app`→`agent`. `cmdutil` imports
`api`/`exitcode` and neither imports it.

## Not done

- No live-server verification. The wire path is the unchanged generated client
  that `app agent add` already exercises; a smoke test would leave a throwaway
  agent plus its system memory in the org.
- `--install-into` takes one App. Installing into several in one run was not
  asked for and would complicate the partial-failure story considerably.
- §2, §3 and §4 of #535 are untouched and remain open there.

## Follow-up found while building

`classifyTransport` cannot currently reach its own gateway-5xx branch. It skips
the transport class when `httpErr.Response.Errors` is non-empty ("a 5xx carrying
real GraphQL errors is the API refusing"), but genqlient **synthesises** a
one-entry `gqlerror.List` holding the raw body whenever that body is not valid
JSON — which is exactly what an nginx/Cloudflare HTML 502 is. So a real gateway
5xx is classified as a refusal (exit 1) rather than `Unavailable` (exit 7).

Out of scope here; filed separately. This PR's `unknown` test therefore uses a
dropped connection, which is classified correctly.
