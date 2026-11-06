# Read-only Orca host plan (cli#1635)

Scope: Xabat CHECKED6672724677 / Eden dispatch82020. `orca host plan --roster-file` inspects; no apply, launcher or session/config/worktree/terminal writer ships. The broader first-host-write fork remains Ada→Holger81920, traced to Jonas811/6628157339. No new platform law or server endpoint. This is not the unbuilt server1615 planTeam/applyTeam protocol.

## Local input and output

Version1 UTF-8 regular roster file, <=1MiB; exactly version/app/workers keys, worker rows worker/repo/family only. Strict duplicate/case/unknown-key detection, 1..200 rows, claude/codex/glm, absolute existing checkout-root directories, no .. segments. Canonicalize path symlinks; roster/binding final-file symlinks refused using O_NOFOLLOW. Nonblocking opened-handle fstat and bounded read prevent FIFO substitution/growth from hanging/unbounding reads. Windows file reads explicitly unsupported; Linux execution is the tested minimum. Repo roots must contain .git; actual git identity checked later. Data strings are never shell-expanded.

The file explicitly selects the App, ignores ambient App defaults, and rejects --app rather than silently overriding it. Narrow HostPlanApp and HostPlanWorker queries on the existing worker(appRef,ref) provider resolve the App id and each worker, one App-id query plus one slim worker query per row (no staff pagination/fallback). Compare returned worker.AppId to resolved App id; reject retired, foreign or duplicate resolved workers before host inventory. Known recorded family disagreement is a conflict, not a model-launch inference. Input ref names are not reconstructed from slugs. Provider failures retain the mapper's exit category but do not echo provider diagnostic prose.

Explicit stable DTO: version/server/app/readOnly/complete/discoveryScope/workers/actions/conflicts/unavailable. Non-null arrays; worker rows id/worker/repo/desired family plus observed worktree paths, terminal handles and recorded session ids. No tokens, prompts, transcripts, binding model/config payload or raw subprocess output. Stable roster order; sorted discovered paths and handles. Text reports the App/server, discovery scope, worker counts and proposed-action/conflict summaries, sanitizing user-controlled strings. Complete means the bounded discovery scope completed, not execution-ready, authorized or currently live.

## Discovery boundary

Do not reuse terminal status: its due-wake read can reserve budget and is outside this slice. Dedicated closed subprocess vectors: `orca terminal list --json`; `git -C <absolute checkout> worktree list --porcelain -z`; git rev-parse --show-toplevel, --path-format=absolute --git-common-dir, --absolute-git-dir. No shell, terminal screen or input, remote/fetch, checkout/reset, setup hook or agent/session invocation. Each process 15s plus1s pipe wait, stdout bounded8MiB, stderr discarded. Child environment allowlist contains PATH/HOME/XDG_CONFIG_HOME/XDG_RUNTIME_DIR/TMPDIR/USER only, plus git no-optional-locks/no-prompts/no-global-or-system-config controls. Tokens/provider variables and injected GIT_CONFIG entries are absent.

Require successful, structurally valid Orca envelope and explicit terminals array; missing/null inventory never means all absent. Git worktree list must name at least one canonical existing path. Confirm actual root/common-git identity. Read bounded git-directory binding files and associate only exact worker-id/App-id; inspect terminal worktree paths as well to find conflicting bindings in another repo. Never associate by path suffix or terminal label. Well-formed bindings naming a worker outside the resolved roster are ignored before validating legacy App/session metadata; malformed JSON or absent worker identity remains unknown and fails closed. Missing worktree directories are ignored only when git explicitly marks that record prunable; no pruning is executed. A valid terminal entry with an unresolved local path produces terminal_path_unresolved and exit 1, not Orca-unreachable exit 10. Discovery failures carry a resource-class reason (git_inventory_unavailable or terminal_path_unresolved) without raw paths/prose. No resource is modified. Terminal identity is compared to desired family when present; unknown identity produces inspect-agent data, not a launch/readiness assertion. A recorded session id is just that, not server liveness. No expected branch is supplied in this roster schema, so the planner neither selects a branch nor claims branch suitability.

No matching binding: propose create-worktree/open-terminal/authorize-session as data with source repo (no invented destination branch/path or executable command). Matching binding but no terminal: propose open-terminal. One matching observed terminal/family: no create action. Wrong App/repo/family or multiple bound worktrees/terminals: conflict/no actions for that worker. Incomplete reads produce unavailable and nonzero; source/refusal categories remain separate from conflicts. No uncertain write can be retried because no executor exists.

## Evidence

Red592de4f8: command surface absent on freshmain3dc5ad6a. Author focused tests cover malformed roster/no external calls; duplicate/foreign identity; late worker refusal; deterministic repeated plan; absent/existing/conflicting resources; empty/null/malformed discovery; raw diagnostic omission; exact read argv and mutating-surface refusal. A new test caught Cobra's help-only success for host apply and fixed it to usage2. Real temporary-git fixture uses a literal shell-like path, exact argv, synthetic binding and a content/path snapshot: reads leave it byte-identical and no injection marker appears. FIFO/symlink file refusal pinned. These are fixture writes only, not host-worker/session operations. Required guard mutants, full tests/lint/race/generated checks and independent seats recorded at freeze.

Limits: snapshot can race external changes; no safe-to-type, live-model, session-availability, idempotent apply, rollback, provider1615, deployment or production proof. Recorded bindings may be stale; proposed authorization must be reconsidered by any future authorized implementation. No macOS execution evidence without a named seat. No general credential scrub claim: raw diagnostics are withheld rather than pattern-scrubbed, and operator-supplied roster references/paths remain explicit data in JSON. Public docs follow after merge/version decision; no release claimed here.

Author code review narrowed the worker read to id/appId/family/retiredAt (no shared WorkerFields briefing/signature/prompt selection). This is a generated operation over existing schema fields, not a new provider or roster listing. Four exact guard mutants (execute-plan, failure-as-empty, drop-repo-identity, shell-interpolation) each failed assertions in a separate throwaway; restored focused controls green, byte-identical restore. Logs and mutations in scratch/cli1635-mutants*.

Request budget: 1 App query + N single-worker queries (2..201 total for 1..200 rows), stopping on first failure, no retries. Worker payload is id/appId/family/retiredAt only, roughly a few hundred bytes per row plus transport envelope; at200 workers tens of KB, estimate not a latency claim. Host subprocess output per call capped8MiB. QA compiled fixture should report request count, total response-body bytes and wall time on a representative roster.

## Author checklist review

Read review parent and applicable Scope/checklists. No generic code-review skill is installed; source correctness pass performed directly.

| Review node | Result |
|---|---|
| a-claim-must-not-outrun-its-evidence | Pass: complete is discovery-only; unavailable never becomes an all-clear |
| a-gate-can-outlive-its-premise | Skipped: no upstream proxy retirement |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host acceptance matrix or law-cited fixture changed |
| ambient-scope-must-report-its-source | Skipped: App required in roster; no ambient App fallback |
| a-mutation-check-can-itself-be-a-no-op | Pass: four exact nonempty edits failed named assertions, restored controls green; equivalent control also recorded at freeze |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no existing domain rule narrowed |
| an-empty-flag-is-not-an-absent-flag | Skipped: empty and omitted roster-file both invalid, no distinct empty semantic |
| an-empty-result-as-a-signal-must-be-unforgeable | Pass: explicit terminal list required; missing/null/list error refused; no paged worker list |
| a-new-meaning-for-an-existing-glyph | Skipped: no existing placeholder repurposed |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Pass: closed argv list; unrelated/non-git terminals never associated by label |
| a-pointer-must-be-followed-not-reasoned-about | Pass: rendered host help and terminal-status help paths checked with built binary |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no newly recommended provider field; existing identity data validated, null family remains unknown |
| assert-the-query-not-the-capture | Pass: generated operation field-exact assertions and selection mutant |
| assert-the-value-the-user-gets | Pass: explicit coded/silent exits and raw DTO pinned; compiled QA independent |
| async-wait-command-contract | Skipped: no polling or asynchronous wait |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: later-worker and repeated-plan fixtures; focused race green |
| a-tidy-argument-is-not-a-checked-one | Skipped: no claim that visibility/identity proves remote liveness or authority |
| backticks-in-flag-usage-become-the-placeholder | Pass: no backticks in roster-file usage; rendered help checked |
| canonical-ref-handling | Skipped: no memory/node ref or upsert, App uses shared CanonicalAppRef |
| classify-by-provenance-before-wrapping | Skipped: new SafeLine text only, no SafeCell sweep |
| classify-security-input-by-structure | Skipped: no API-request security classifier |
| confirm-prompt-tells-the-truth | Skipped: no confirmation or mutation |
| entity-fields-not-display-labels | Pass: id plus input worker ref; App is a canonical ref, not a display label |
| enumerate-the-paths-before-promising-a-guarantee | Pass: input/provider/git/Orca/binding/render failures enumerated; no executor |
| fetch-only-what-the-command-needs | Pass: minimal App-id and four-field worker operations; 1+N request budget stated |
| gate-on-the-condition-not-the-flag | Skipped: no override or behavior proxy flag |
| help-text-matches-changed-behaviour | Pass: Orca group expanded from terminal-only to host read plan; agentic contract updated |
| improving-an-answer-widens-a-domain | Skipped: no prior JSON uncertainty retired |
| map-new-server-error-codes | Skipped: no new server codes, existing mapped categories retained |
| mutate-with-the-original-input | Skipped: new feature, not a reported product-defect fix |
| needs-a-spec-when-it-decides-a-rule | Pass: gate FIT: host-local roster/DTO only, no platform law or write grant |
| no-hand-parsed-ref-labels | Pass: no display label derived from URN, server identities compared |
| no-rmw-sugar-over-wholesale-writes | Skipped: no writes or read-modify-write |
| old-server-graphql-document-compatibility | Skipped: new command uses existing schema fields; no old document widened |
| omit-unset-optional-args | Skipped: new identity query arguments required, no optional input refresh |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no bot feedback |
| regenerate-after-graphql-edit | Pass: make generate, committed generated client; repeated freshness check |
| separate-streams-cannot-test-order | Skipped: no cross-stream ordering claim |
| stable-json-dto | Pass: package-local DTO; every array raw/non-nil discriminator including fulfilled plan |
| sweep-by-identifier-not-by-prose | Skipped: no old surface renamed/removed |
| sweep-the-subject-not-the-removed-rule | Skipped: no old rule removed |

## Deployment-scope correction before freeze

Self-review of the coverage checklist identified the cross-server clone case: App/worker IDs are unique within a deployment, not across restored/cloned deployments. Red ee8a5cdd accepted a binding from other.example as an existing resource despite identical ids. The fix also compares binding.server to the selected HTTP(S) GraphQL endpoint. Normalize scheme/host/default port/trailing base slash while preserving endpoint path; no DNS-alias equivalence is inferred. Target URLs with userinfo/query/fragment refuse locally without echoing them. DTO server names the sanitized endpoint. Missing/invalid binding server is unavailable/no action, not absent; mismatch is conflict/no action. Normal same-server spellings remain a positive control. This adds no provider admission policy, live-session proof or host writes. Its exact guard mutant and restored controls are recorded with the other evidence.

## Review fix cycle (original frozen head 95702ce0)

Discovery scope is explicitly `roster-repositories-and-reported-terminals` in JSON, help and text. The planner does not search arbitrary host directories or discover a binding in another repository with no reported terminal. An absent observation is absence within this scope, never a global assertion; complete means that bounded scan completed. Future execution cannot treat this data as authorization or a host-wide census. A terminal's reported path currently associates only at an exact worktree root; subdirectory terminals are not inferred as the same worker (Gaby P3-1, deferred, Jane owns a follow-up before any execution consumer).

LOCAL P2-1 and BLIND P2-1/2: explicit prunable-record handling plus unrelated legacy-binding identity filtering; unresolved terminal paths remain fail-closed with exit 1/resource-class diagnostics. Real git fixture creates a linked worktree, reads its binding from the resolved per-worktree git dir, removes its directory and proves discovery does not prune/change surviving metadata. Unmarked missing paths and unreadable/unknown identity remain incomplete. LOCAL P2-2 and security P3-2: foreign-App fixture requires conflict and no actions. BLIND P2-3: bounded scope named above, no broader host-census claim. BLIND P2-4: decision table pins App/family/multiplicity conflicts, open-terminal/inspect-agent, retired/foreign refusal before discovery, nested worker keys and linked worktrees; independent Cooper's compiled 104-case QA additionally exercised these decisions at the original head. Original QA is not automatically asserted at this production fix.

Security P2-1: a real 9,000,000-byte subprocess reproduces the promoted bytes.Buffer.ReadFrom bypass. The buffer is now a named field, exposing only the capped Write; overflow refuses without returning bytes. Security P3-1/3: real child environment sentinel/config controls and retired-worker decision pin added. Red commits e0b4fc9e/a62b79c8/0753c31c precede production fixes. Terminal-exit regression also fails at a detached original head. Logs in author's scratch retain exact commands/results; each changed guard mutant is checked before refreeze.

BLIND optional findings dispositions (original numbering):
- 5: exact normalized generated operation assertions replace denylist-only confidence; no unneeded selection is permitted.
- 6: independent compiled QA checks both output streams; author fake helpers' returned-error/stderr strength remains a named limit. No raw provider/subprocess error is returned by this command.
- 7: fixture repository helper now canonicalizes temporary paths. This is not macOS execution proof.
- 8: Windows runtime is explicitly unsupported; cross-build only. Unix real-process fixtures have the build tag; remaining planner tests are Linux-tested, not a Windows CI promise.
- 9: system/global git config including safe.directory is suppressed deliberately; repositories requiring a foreign-owner trust override can fail incomplete. No trust override is invented.
- 10: tested git is 2.47.3; no earliest-version floor inferred. Older-tool compatibility/version preflight deferred (Jane, before widening supported hosts); failures remain loud, no relaxed environment fallback.
- 11: resource-class hints added for inventory failures; richer row/file local diagnostics deferred (Jane, next UX pass), unfiled to keep this bounded fix focused.
- 12/13: neutral flag wording and explicit-empty --app distinction deferred (Jane, next flag-validation slice); the explicit roster remains authoritative in every case, ambient App is not used.
- 14: text now names server and discovery scope; it remains a count/action summary rather than full JSON resource rows. Full path/handle text listing deferred (Jane, next UX pass).
- 15: each subprocess has a 15s timeout plus 1s pipe wait; there is no total-roster deadline. Many distinct repos can take many sequential bounds. Overall deadline deferred (Jane, before any interactive execution consumer); loopback QA cost is not a host-latency guarantee.
- 16: naming/file refactor deferred (Jane, after behavior settles); no behavior risk justifies more changed lines now.
- 17: worker/ref mismatch corrected; prose spacing cleaned only in touched contract text, broader style pass deferred.
- 18: inventory regenerated from clean fix-parent; group rubric reviewed again. Additional examples deferred to public documentation follow-through.

Source answers/limits: team/state.go writes/reads bindings under resolved git dir and documents pre-399 missing App ids; only an unrelated parsed worker id is ignored, never unknown identity. api.Endpoint appends /graphql, and current binding writer stores Factory.Server's base URL; arbitrary endpoint spellings are not inferred equivalent. CheckKeys recurses through typed worker arrays and exact key names, now pinned. No source-only check proves real Orca inventory completeness, reduced-environment deployment compatibility, provider retired-field admission, or a live session's authority. Named independent fake evidence stays fake; session ids are recorded metadata, never accepted by this command as credentials. No newly invented provider/deployed-version floor or live-host proof is claimed. Those limits and the absence of an executor remain explicit at handoff. Optional items above remain unfiled with owner/trigger, not acceptance waivers.
