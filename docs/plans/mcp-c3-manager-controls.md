# MCP cut C3: manager controls, first slice

Refs #1336 and hadron-server#2937. This slice adds team pause/resume and worker
change-agent only. Remaining MCP-cut wrappers stay outside this PR.

The typed operations use the existing schema snapshot. Both mutations remain
outside the authored-write whitelist: the signed-in App manager acts as a
person even in a bound worktree. Authorization, casting concurrency, agent
installation and grant checks stay on the server. No local permission cache
or mutation retry is added.

Pause/resume resolve App scope by the existing --app/context/binding rules.
Their explicit JSON DTO is {app, paused, teamPausedAt}; paused is derived from
the returned nullable timestamp. Idempotent pause preserves the timestamp;
resume does not lift worker self-pauses or independent stop signals. Neither
command claims to interrupt or resume a running worker.

Change-agent accepts a worker name/id/URN and an agent id/full URN. Agent
validation checks shape only; the server resolves it. Confirmation precedes
all requests and discloses clearing the boot-prompt pointer and replacing
generated tickers. Non-interactive callers require --yes. The returned worker
uses the existing worker DTO; new sessions use its new casting, open sessions
finish under their old casting. A deployed server containing #2504 is required
for open-session changes; release/deploy is outside this build.

Refusals retain serverCode and the shared exit classification. Three explicit
state codes now map to conflict (5): WORKER_CASTING_STALE,
WORKER_AGENT_UNCHANGED and WORKER_AGENT_HAS_NO_ROLE. Unknown/null write results
are errors, never fabricated success. A lost reply does not cause a retry.

The first commit contains runtime-failing command/wire tests on main. They
cover App scope, returned-state truth, prompt cancellation before any request,
manager headers with an authored-write positive control, null payloads and
refusal exit/code preservation. Independent QA must run a compiled binary on
one private disposable stack; UX, LOCAL, BLIND and security seats are required.
Vera's help/spec fit is also required before readiness. No production writes
are part of author verification.
