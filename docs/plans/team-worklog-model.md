# Design as built: event-time model on team worklog milestones (#746)

This CLI follows the merged server contract in hadron-server#1401 at
`17d6d96d8c29babeec80e779f150519932275597`. The schema snapshot and
generated client were refreshed from that commit.

## Recording

`team session log --model <name>` reports the LLM that produced this one
milestone. An explicit value is sent through `recordTeamWork(model:)`; an
omitted flag leaves the GraphQL variable **absent**, so the server uses the
recording session's `llmModel` when it has one. The CLI never copies the model
from the local worktree binding: that value can predate a model switch. Empty
or whitespace-only explicit values refuse before a request. The returned
`TeamWorkItem.model` is the receipt's `model` (null means unknown), independent
of the artifact host in `tool`. Attribution is reported, not verified by the
server against the process that produced the work. `session start --model`
still sets the session's initial model.

An older server has neither the argument nor the result field. A schema
validation error about `model` retries an ordinary, omitted-model milestone
once through the legacy generated operation. An explicit `--model` fails with
an actionable unsupported-server message and sends no legacy worklog mutation.
Other errors never take the fallback. On a pre-App binding, `--model` refuses
before touching Session because there is no worklog destination.

## Reading

The provenance query selects each matching worklog row's `model`, grouping
the returned milestones beneath their session in JSON as `worklog[]`. A
session can have several milestones with different models; they stay separate
and old or unknown rows retain `model: null`. The human view labels the
existing session value `SESSION MODEL` and adds a milestone table with
`REPORTED MODEL` and `ARTIFACT TOOL`. A schema validation error on the new
field retries the page through a legacy operation; following pages use that
legacy operation directly. Session resolution still runs once per distinct
session, including unreadable-session stubs.

## Evidence

Command tests cover explicit, omitted fallback and unknown receipts; a
changed model across three rows in one session; an old server's ordinary
record/read fallback and explicit-model refusal; and blank-model refusal.
The full Go suite, `go vet ./...`, and `go tool genqlient` pass against the
merged SDL. The server's own #1398 tests establish event-time snapshot
and historical-row behavior; the CLI preserves and renders those values.
