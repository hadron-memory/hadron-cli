# Census-backed unidentified terminals — cli#2193

Scope and acceptance: [issue #2193](https://github.com/hadron-memory/hadron-cli/issues/2193),
[CHECKED T1 gate](https://github.com/hadron-memory/hadron-cli/issues/2193#issuecomment-6935829214).
The gate adopts worktree-level proof because Orca's list has no terminal-to-PID
association. Keep `AgentTerminals` and `UnidentifiedTerminals`; no field rename,
server API, session lifecycle, process signalling, or host installation change.

## Observer

Identified terminals count as before, connected or disconnected. Unidentified
terminals initially count too. When there are any, use C5's strict Linux census
(`ProbeWorktreeAgents`), bracketed by the same managed-daemon readiness check.
Missing readiness, unreadable processes, invalid/ambiguous proof and non-Linux
hosts retain every unknown terminal. The census includes the caller/ancestors
and uses #2196's identity-checked foreign-UID exclusion, never the best-effort
duplicate-cleanup scanner.

The proof is about counts in a worktree, not a specific terminal's tree: if the
complete census has no more agents than the identified terminals already
account for, the unknown rows add no possible agents. Otherwise ALL unknown
rows stay counted; no arbitrary selection of a tab to call empty. Thus one
known agent plus an old shell and exactly one census process reads one, while
an unidentified terminal with an agent process still reads present. No evidence
is cached between observations. The existing C3 start guard remains responsible
for fresh admission before creating an agent; host-up observation is a snapshot.

## C5 cleanup

Cleanup is opt-in through `orca_launch.cleanup_dead_tabs: true` (default
false). Wolfgang narrowed the tab policy and accepted this opt-in in team chat
#110846 after the first review. No configuration is changed by this PR.

`Runner.AfterLaunch` runs after the relaunch outcome notice, with original
admission released and the new agent tracked. The callback then reacquires
C3/C5 admission and the binding lock, rechecks the binding and fresh desired
state/target, and only then invokes cleanup. Failed fresh authority retains
the tab. Cleanup panics are caught with a code-only local receipt. Slow or
panicking cleanup therefore cannot suppress or consume the earlier notice's
budget. Only a successful relaunch invokes it, including C12's relaunch path;
a healthy worker or failed relaunch does not clean tabs.

Orca's `terminal close --tab` closes every pane in a tab. Require a complete
fresh list: the tracked handle must still be in another tab in this worktree;
every pane in the candidate tab must be disconnected, unidentified, in this
worktree, and not tracked. Missing tab IDs, duplicate handles, known panes, changed identity,
foreign-worktree panes and a missing tracked handle leave the tab open. A failed
Orca subprocess or cancelled call invalidates stdout even if it contains a
complete-looking successful JSON envelope; list/close positive controls pin this. Before each close, re-run the strict census, verify the launched agent's
pinned identity, and re-list last to validate the tab immediately before closing. The accepted proof is deliberately stricter than merely tree
membership: exactly the pinned launched PID and no other agent match. A nested
agent/tool subprocess therefore defers cleanup until another relaunch rather
than being guessed safe. Wolfgang accepted this conservative limit in team chat #110808. QA must
check real Claude and Codex relaunches on scratch workers for nested agent
matches; if the ordinary tree prevents cleanup, file the ancestry-proof
follow-up with that evidence.
A disconnected flag alone never authorizes a close. Connected shells may hold
a human's non-agent build, ssh or editor, so they always stay open. This
supersedes the original gate's connected-shell permission per #110846. Non-Linux never closes an unidentified tab, even with an
injected positive proof.

The complete post-notice cleanup pass has a cooperative 30-second context deadline, including fresh authority reads. It
performs no background retries. Local `relaunch-terminal-cleanup` events record
`DEAD_TAB_CLOSED`, `TRACKED_TERMINAL`, `IDENTIFIED_TERMINAL`, `TAB_UNPROVEN`,
`CENSUS_UNPROVEN`, `CONNECTED_TERMINAL`, `TERMINAL_LIST_UNAVAILABLE`,
`TERMINAL_CLOSE_FAILED`, `CLEANUP_DISABLED`, `CLEANUP_UNAVAILABLE`,
`CLEANUP_ADMISSION_UNAVAILABLE`, `CLEANUP_BINDING_UNPROVEN`,
`CLEANUP_TARGET_UNPROVEN` or `CLEANUP_PANIC` with the
terminal handle and worker/session IDs. They neither alter the successful
relaunch outcome nor add a server report or event kind. A refused close is a
cleanup failure, not a failed launch. The normal server relaunch notice and S10
row stay unchanged. No arbitrary stderr/argv/proc contents enter these events.

## Evidence and limits

Red commit `6a02725f`: the one-agent/old-shell control still counts two, and a
proven bare shell counts one. The first C5 test used the direct sweep helper;
its fixture needs the full Step to resolve worker refs. The corrected harness
pins one cleanup after successful relaunch, none on the first observation or
for the later healthy worker. Author tests use synthetic terminal/census seams;
they do not establish a real Orca close or host namespace authority. Independent
CLI QA with Hugo uses a scratch/leave-alone worker, never a pilot worker before
2026-11-23. This PR is not installed on orca1 by its author.

No new platform spec: this implements the gate's local process/terminal proof
and local diagnostics, without changing a contract used by another repo.

Author validation: `make test`, `make lint` (0 issues), `make build`, focused
race tests for #2191/#2193 and relaunches, Darwin/arm64 and Windows/amd64 builds
pass. The #2191 command controls now explicitly make managed-host evidence
unavailable rather than consulting the developer's live host. Four precise
mutants were applied with an exact-one-match assertion and restored in finally:
disable observer absence proof (positive controls fail), bypass cleanup census
proof (both unknown-census controls fail), bypass whole-tab protection (tracked,
known-pane, foreign-pane, changed-list and missing-track controls fail), disable
cleanup hook (corrected C5 fixture fails). Each compiled and failed the named
new test; an equivalent `proof.Proven == true` rewrite passes.

### Author checklist walk

| Review node | Result |
|---|---|
| a-claim-must-not-outrun-its-evidence | Pass: complete census only, uncertainty retained, no cached proof |
| a-gate-can-outlive-its-premise | Skipped: no upstream mechanism retired |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host contract matrix edit |
| ambient-scope-must-report-its-source | Skipped: scope resolution unchanged |
| a-mutation-check-can-itself-be-a-no-op | Pass: exact-one-match edits, named assertion failures, equivalent survives |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Pass: positive empty and negative uncertain cases; old controls explicitly withhold proof |
| an-empty-flag-is-not-an-absent-flag | Skipped: no flag change |
| an-empty-result-as-a-signal-must-be-unforgeable | Pass: no best-effort scanner, no partial/error proof |
| a-new-meaning-for-an-existing-glyph | Skipped: no glyph change |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no process exemption added; foreign-UID rule reused unchanged |
| a-pointer-must-be-followed-not-reasoned-about | Pass: issue/gate read and linked; API/helper names inspected |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no newly introduced provider field; TabID already used by the existing sweep |
| assert-the-query-not-the-capture | Skipped: no GraphQL selection change |
| assert-the-value-the-user-gets | Pass: existing command DTO assertions plus two carried boundary rows; seam tests labelled as unit tests |
| async-wait-command-contract | Skipped: no server async wait change |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: initial and fresh lists; corrected full-Step C5 fixture; race checks |
| a-tidy-argument-is-not-a-checked-one | Pass: worktree counts per gate; no terminal-to-PID inference; shared-tab guard |
| backticks-in-flag-usage-become-the-placeholder | Skipped: no flag usage change |
| bind-terminal-evidence-to-target-process | Pass: gate adopts worktree-only absence; never attributes idle screen to a PID; pinned own-agent proof |
| canonical-ref-handling | Skipped: no ref parser change |
| classify-by-provenance-before-wrapping | Skipped: no output escaping change |
| classify-security-input-by-structure | Skipped: no request classifier change |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt change |
| entity-fields-not-display-labels | Skipped: no DTO field added |
| enumerate-the-paths-before-promising-a-guarantee | Pass: candidate, connected, list, shared-tab, census, platform, context and close failures enumerated above |
| fetch-only-what-the-command-needs | Pass: no new selection; one existing desired-state read only for enabled successful cleanup, local census for unknown rows and actual cleanup |
| gate-on-the-condition-not-the-flag | Skipped: no flag-derived gate |
| help-text-matches-changed-behaviour | Pass: host-up help and agentic-usage updated |
| improving-an-answer-widens-a-domain | Pass: field names and status codes preserved |
| map-new-server-error-codes | Skipped: no new server error |
| mutate-with-the-original-input | Pass: gate's null-old-tab plus known-new-agent control; no claim of verbatim host capture |
| needs-a-spec-when-it-decides-a-rule | Pass: local proof/cleanup only; no cross-repo contract decision |
| no-hand-parsed-ref-labels | Skipped: no ref label change |
| no-rmw-sugar-over-wholesale-writes | Skipped: no whole-list write |
| old-server-graphql-document-compatibility | Skipped: no document edit |
| omit-unset-optional-args | Skipped: no mutation input edit |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no peer verdict yet; required seats pending |
| recovery-must-not-block-the-host-loop | Pass: same owned recovery job; channel-stalled cleanup permits next collection; admission held for cleanup |
| regenerate-after-graphql-edit | Skipped: no schema/query edit |
| separate-streams-cannot-test-order | Skipped: no cross-stream ordering assertion |
| stable-json-dto | Pass: existing hostEventDTO unchanged, local kind/reason documented |
| sweep-by-identifier-not-by-prose | Skipped: no public identifier removal/rename |
| sweep-the-subject-not-the-removed-rule | Pass: observation comments, command help, agent contract, plan checked together |

No new GraphQL operations or response fields are introduced. Host-up retains its
existing single host-registration read per run plus the existing desired-state
and optional S10 calls. Added local work per worker with unknown rows: up to
two readiness checks and one complete /proc census under a 10-second cooperative
deadline. Enabled C5 cleanup adds one fresh existing desired-state snapshot per successful
relaunch before acting, then one full terminal list plus one fresh list and one census per
candidate tab (a truncated Orca list can require its existing second list call),
only after a real relaunch, with a 30-second cooperative deadline for the pass.
This is request-path accounting, not a latency benchmark or a real-host QA claim.

### Review fix cycle

Jerry F1: connected tabs and any tab with a connected pane are retained,
pinned on both the initial and fresh lists. Jerry F2: notice precedes cleanup;
cleanup then takes fresh admission/binding/desired-state authority, catches
panics and has its own bounded pass. Jerry F3: config opt-in is off by default.
These follow Wolfgang #110846; the original permissive connected-tab and
before-notice prose above is superseded wherever described as history.

Jonas F1: tests drive the actual production cleanup adapter and proof closure
through census and verifier seams, covering other/extra/nested PIDs, census
errors/unproven evidence, exited/changed identities and the sole pinned PID.
Replacing that closure with true and bypassing verification must fail them.
Default opt-in wiring is tested by driving C5 to its real terminal client;
notice ordering, post-notice authority changes and panic isolation are pinned.
Other optional test/diagnostic/style findings are tracked as deferred work.

Hugo's real relaunch/close leg remains NOT RUN until after the pilot (around
2026-11-23), on a scratch/leave-alone worker, with evidence to #2205. Wolfgang
accepted Charles's logic-leg scope plus this named follow-up (#110840/#110846);
no live execution is claimed by that coverage decision.

Remaining optional diagnostics, observer wiring pins and plan/test polish are
tracked in [#2208](https://github.com/hadron-memory/hadron-cli/issues/2208).
The fix-cycle mutants (each exact-one-match, compiled, then restored) reject a
production proof forced true, bypassed verifier, default-on cleanup, a connected
pane appearing in the last list, and cleanup moved ahead of the notice. An
equivalent Proven==true rewrite survives. The new tests execute the production
proof closure and the default cleanup wiring, rather than replacing the safety
predicate or cleanup adapter wholesale.
