# Login skill onboarding (#508)

This adds the October 24 gate's offer after successful login. It consumes the
existing readable task-node export set. No skill bundle, new command, default
set, server rule, corpus move or access grant is added.

The login path detects Claude Code from ~/.claude or claude on PATH, and Codex
from ~/.agents, ~/.codex or codex on PATH. Detection controls the exact host
list passed to the shared export writer. Explicit `skill export` keeps its
existing all-host behavior.

The planner is read before prompting through a class-only projection (no rendered
skills or filesystem writes), so an empty or
all-disabled set produces a no-enabled-skills message. After consent the writer
re-plans with current local file facts and applies its existing safe I/O and
ownership logic. No force or prune override is supplied. Hand-edited, foreign
or legacy files retain the writer's normal refusal. Current files need no
restart; actual written/moved/removed files get its existing restart hint.

Interactive login asks once, default no. Non-TTY/JSON gives a command on stderr.
`--install-skills` accepts explicitly, including non-TTY/JSON; the JSON login
object then embeds the export report under `skills`, including partial refusal
reports. No second JSON value is printed. `--no-install-skills` and config
`skills.installPrompt=false` silence automatic installation; an explicit install
wins over config. The two flags are mutually exclusive. An automatic offer's
failure does not undo/fail the successful login; explicit installation failures
return nonzero and explain that the login token is already stored.

The export client is constructed from the newly stored token, just like the
login account read (#1860). It bypasses ambient HADRON_TOKEN and impersonation
for both preflight and export reads. Other commands retain their ordinary
credential precedence.

Acceptance uses synthetic HTTP + temporary filesystem + mock keychain, with
TTY-aware IOStreams and fake browser handoff. The separate red-first commit
pins the missing UX plus detected-host selection and stored-account identity;
environment read seams avoid changing HOME or touching installed skills.
Private-cache mutants must independently break host selection and token identity.
Independent QA runs the CLI cases required by the gate. No native agent restart
or production login is needed to establish the client contract.

Expected normal request counts (H detected hosts, at most two): login's existing
one AuthContext read, H small classification reads, then H full export plans
only after consent/explicit installation. Decline or nothing-enabled skips the
export plans; non-TTY/JSON automatic hints add no skill-plan requests. The
writer's existing old-server revision compatibility retry is unchanged. The
classification payload grows with declaration count but includes only class;
rendered bodies are returned only for the consented export. Independent CLI QA
records actual request count/response bytes for its fixture size.

The availability query stays in skills.graphql with its existing shared-input
omitempty directives. A wire assertion forbids force/scopeRef/memories from
being sent as null or overrides. Generation must leave existing shared types
unchanged; the new generated diff is additive only.
