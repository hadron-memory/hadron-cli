# How to write an intake adapter

`hadron team task find-untracked` lists the outside items (issues, tickets)
that no task of your App holds yet, and with `--track` claims some of them as
tasks. Hadron never talks to your tracker. It asks a small local program your
team owns, the **intake adapter**, and the adapter talks to the tracker. Without
an adapter the command refuses to run.

This page tells you what an adapter is, how the CLI calls it, what each call
receives and answers, and how to try one. It ends with a sample adapter you can
build and run in a minute. The sample is a starting point, not a complete
adapter.

If your tracker is GitHub, an adapter already exists:
[hadron-task-adapter-github](https://github.com/hadron-memory/hadron-task-adapter-github).

## What an adapter is

- An executable (a script or a compiled program, in any language) installed on
  the host where the CLI runs. Your team owns it, installs it and keeps it up
  to date.
- It holds the tracker credential, under a name of its own. Hadron never sees
  that credential and never calls the tracker.
- It answers a small set of named calls. Which calls exist, and what they mean,
  is the intake adapter contract, `cor:agt:120:04` in Hadron's platform specs.
  This page restates the shipped contract as CLI contract version 1 speaks it.
  Parts of it are still Proposed in the spec, so read this page as "what the
  shipped CLI speaks", not as a standard.

Tell the CLI where it is with `--adapter <executable>`, or set
`$HADRON_INTAKE_ADAPTER` (used only when no `--adapter` is given).

## How the CLI calls it

Every call is one run of the executable:

```
<executable> <call>
```

- **One argument**, the call's name (`describe`, `list-untracked`, …). The CLI
  runs the file directly, never through a shell.
- **The request is one JSON object on stdin.** It always carries
  `"contractVersion": 1`, plus the call's own fields below.
- **The answer is one JSON object on stdout, and exit status 0.** No other
  text may follow the object (a trailing newline is fine). Write `{}` when a
  call has nothing to say.
- **Any other exit status is a refusal.** The CLI reports it with the exit
  status and the start of your stderr, made one terminal-safe line, so put the
  reason there.
- **Your environment arrives without `HADRON_*`.** Every variable whose name
  starts with `HADRON_` (in any case) is removed before the adapter runs, so a
  Hadron token never reaches it. Name your own settings differently.
- **Limits.** stdout is capped at 1 MiB per call and a call at 60 seconds. A
  page holds at most 100 items, and a list at most 50 pages. An external id is
  at most 512 characters, a title or the adapter's name at most 1024 bytes, a
  cursor at most 4096 bytes. An answer over a limit is refused, and so is one
  that is not valid UTF-8 or not exactly one JSON object; nothing is cut down to
  fit.
- **Your output is untrusted.** The CLI parses it strictly and prints titles
  terminal-safe, so a hostile title cannot move the cursor or clear the screen.

## The calls

The contract's **core calls**, which every adapter is meant to answer, are
`describe`, `list-untracked`, `mark-tracked`, `untrack` and `get`. `report` is
one of the **optional** calls (with `changed-since`, `normalize` and `check`).

The shipped CLI calls five of them today: `describe`, `list-untracked`,
`mark-tracked`, `untrack` and `report`. `get` belongs to the contract's core,
but the CLI does not call it yet.

### `describe`

The first call of every run, before the CLI reads your binding, the App or
Hadron.

- Request: `{"contractVersion": 1}`
- Answer:

  ```json
  {"name": "my-tracker", "contractVersion": 1,
   "calls": ["describe", "list-untracked", "mark-tracked", "untrack"],
   "filters": [], "order": "oldest first"}
  ```

  - `name`: shown in the CLI's output; must not be blank.
  - `contractVersion`: the version you speak. If it is not 1, the CLI exits
    **15** at once: nothing else is asked of the adapter and Hadron is not
    touched. Upgrade the adapter or the CLI.
  - `calls`: every call you answer. A run needs `list-untracked`, and `--track`
    needs `mark-tracked`; without them the CLI exits 2 and claims nothing (the
    message says which call the adapter does not answer). A call you leave out
    is simply never made. `--track` with no one to hold the claim also exits 2,
    with a different message that asks you to pass `--for` or `--assignee`.
  - `filters`, `order`: optional metadata. The CLI reads them but does not use
    them yet.
  - `notMine` (optional, default `false`): see `mark-tracked` below.

### `list-untracked`

One page of the items you do not (yet) know to be tracked.

- Request: `{"contractVersion": 1, "limit": 20, "cursor": "…", "for": "Marco"}`.
  `cursor` is absent on the first page. `for` is present only when the user
  passed `--for <worker>`; return that worker's items, as your tracker
  understands it. `limit` is the page size the CLI asks for: the user's `--limit`
  raised to 20 if it is lower, so a `--limit 1` run, or a run with no `--limit`
  at all, asks for 20, and `--limit 50` asks for 50. The CLI refuses a
  `--limit` above 100 before it calls your adapter, and a page never holds
  more than 100 items. The CLI stops reading once it has enough, so answer up
  to `limit` items a page and let it stop.
- Answer:

  ```json
  {"items": [{"externalId": "https://tracker.example/acme/app/issues/1",
              "title": "Fix the login timeout"}],
   "nextCursor": "…"}
  ```

  - `externalId`: the item's canonical URL. The CLI writes it into Hadron
    exactly as you give it, and Hadron compares it exactly, so always give the
    same item the same URL.
  - `nextCursor`: absent or `null` when the list is done. It is opaque: only
    you read it. The CLI hands back the one you gave and never builds an
    offset, because the set shrinks while others claim. Never repeat a cursor
    you already gave in this list; the CLI refuses a repeat as a loop.

You decide what "not known to be tracked" means; it can be "every open item".
It does not have to be exact, because **Hadron decides what is tracked**: the
CLI checks every listed item against the App's tasks and drops the ones a task
holds. A task holds its item even when it is finished or cancelled, until it
is released (`hadron team task release`).

### `mark-tracked`

Called after the CLI's create won a claim, so the tracker can show it. Your
mark is your own choice (a label, a comment, an assignee) and it never decides
whether an item is tracked; Hadron does.

- Request:

  ```json
  {"contractVersion": 1,
   "externalId": "https://tracker.example/acme/app/issues/1",
   "task": {"id": "…", "handle": "AT0.12.1", "portalUrl": "https://…"},
   "worker": {"id": "…", "name": "Marco", "role": "cli-engineer", "model": "…"}}
  ```

  `portalUrl` is the task's page exactly as the server prints it, and absent
  when the server prints none; the CLI never builds one. The worker's `role`
  and `model` are present only when the claim goes to the caller's own bound
  worker; for a claim made for someone else (`--assignee`, `--for`) the
  adapter gets only `id` and `name`, so treat both as optional. After a
  reassignment the CLI calls `mark-tracked` again for the new worker, with only
  its `id` and `name`.
- Answer: `{}`.

If your answer is a refusal on a `--track` claim, the claim still stands: the
task is created and the run exits 1 with `marked: false` and the reason.

`notMine` is the CLI's behaviour, not a ruled part of the contract: an adapter
whose `describe` says `"notMine": true` may answer `mark-tracked`, `untrack` or
`report` with exit status **20** for an external id that is not its item. The
reconcile pass then records the id and moves on instead of retrying it. In the
`--track` claim, exit 20 is an ordinary refusal.

### `untrack`

Called after a task released its item (`hadron team task release`) and no task
of the App holds it any more, so you can remove your mark.

- Request: `{"contractVersion": 1, "externalId": "https://…"}`
- Answer: `{}`.

### `report` (optional)

Called after a task holding the item was cancelled, and only if `describe`
lists `report`. What you do with it on the tracker is your choice.

- Request: `{"contractVersion": 1, "externalId": "https://…", "task": {"id": "…", "handle": "…", "portalUrl": "…"}, "state": "CANCELLED"}`
- Answer: `{}`.

### `get`

One item, given its external id. Part of the contract's core, not called by the
CLI yet; its shape is still Proposed in the spec (the item's title, body,
state and URL).

## How the CLI uses the calls

Every run (`find-untracked --adapter …`, with or without `--track`):

1. `describe`, and the version check.
2. **Reconcile.** The CLI reads from the server what changed on the App's tasks
   that hold an outside item since its last run, and tells the adapter: a
   reassignment calls `mark-tracked` again, a release calls `untrack` only when
   no task of the App, finished or cancelled included, still holds the released
   id, and a cancel calls `report` if you list it. A finish, a hand-back or a
   first assignment calls nothing. Its position is kept in `intake-cursors.json` beside the CLI's
   config, per server, App and adapter, so nothing is told twice. A refusal
   holds the position there, and the next run retries that change.
3. `list-untracked`, page by page, until the list ends or `--limit` is reached.
4. With `--track`: one create per unheld item; the create is the claim. If
   another caller won the item first, it is reported as lost and the next item
   is tried. Each won claim is then told to the adapter with `mark-tracked`.

## Try the sample

The sample below answers `describe`, `list-untracked` and `mark-tracked` over a
local file of items. It needs no tracker and no credential. It is enough for a
list and for a `--track` claim, but it is **not a complete adapter**: it leaves
out the core calls `untrack` and `get`, so a released item is not told to it.
Its `mark-tracked` marks nothing; a real adapter writes its mark on the tracker
there.

It is Go so that it runs the same everywhere the CLI does; an adapter can be
written in any language. If you write yours in Go, the package
`github.com/hadron-memory/hadron-cli/pkg/intakecontract` holds the exact checks
the CLI applies to `describe` and `list-untracked` answers, for your tests.

`items.json`, the "tracker" (put it beside the built adapter, or name it with
`$INTAKE_SAMPLE_ITEMS`):

<!-- sample: items.json -->
```json
[
  {"externalId": "https://tracker.example/acme/app/issues/1", "title": "Fix the login timeout", "for": "Marco"},
  {"externalId": "https://tracker.example/acme/app/issues/2", "title": "Document the export format", "for": "Iris"},
  {"externalId": "https://tracker.example/acme/app/issues/3", "title": "Retry failed uploads"}
]
```

`main.go`, the adapter:

<!-- sample: main.go -->
```go
// A sample intake adapter for `hadron team task find-untracked` over a local
// items.json. A starting point, not a complete adapter: it does not answer
// untrack or get.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type item struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	For        string `json:"for,omitempty"` // the sample's own field
}

type request struct {
	ContractVersion int    `json:"contractVersion"`
	Cursor          string `json:"cursor"`
	For             string `json:"for"`
	Limit           int    `json:"limit"`
	ExternalID      string `json:"externalId"`
}

var calls = []string{"describe", "list-untracked", "mark-tracked"}

func main() {
	if len(os.Args) != 2 {
		fail("usage: intake-adapter <call>, with the request as JSON on stdin")
	}
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fail("the request is not JSON: " + err.Error())
	}
	if req.ContractVersion != 1 {
		fail("this adapter speaks contract version 1, not " + strconv.Itoa(req.ContractVersion))
	}
	switch os.Args[1] {
	case "describe":
		answer(map[string]any{"name": "sample", "contractVersion": 1, "calls": calls,
			"filters": []string{}, "order": "file order"})
	case "list-untracked":
		answer(list(req))
	case "mark-tracked":
		if req.ExternalID == "" {
			fail("mark-tracked needs an externalId")
		}
		// A real adapter writes its mark on the tracker here, for example a
		// label or a comment linking the task's portalUrl.
		answer(map[string]any{})
	default:
		fail("this adapter does not answer " + os.Args[1])
	}
}

// list returns one page of the items file, paged by a cursor only this
// adapter reads: here, the index of the next item, as text.
func list(req request) map[string]any {
	all := load()
	start := 0
	if req.Cursor != "" {
		n, err := strconv.Atoi(req.Cursor)
		if err != nil || n < 0 || n > len(all) {
			fail("unknown cursor")
		}
		start = n
	}
	limit := req.Limit
	if limit < 1 || limit > 100 {
		limit = 100
	}
	page := []item{}
	i := start
	for ; i < len(all) && len(page) < limit; i++ {
		if req.For == "" || all[i].For == req.For {
			page = append(page, item{ExternalID: all[i].ExternalID, Title: all[i].Title})
		}
	}
	out := map[string]any{"items": page}
	if i < len(all) {
		out["nextCursor"] = strconv.Itoa(i)
	}
	return out
}

func load() []item {
	path := os.Getenv("INTAKE_SAMPLE_ITEMS")
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			fail(err.Error())
		}
		path = filepath.Join(filepath.Dir(exe), "items.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err.Error())
	}
	var all []item
	if err := json.Unmarshal(b, &all); err != nil {
		fail(path + ": " + err.Error())
	}
	return all
}

func answer(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		os.Exit(1)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}
```

The sample's `fail` exits 2 because it chose to: the contract gives an adapter's
refusal no code of its own (any non-zero status is one), and the CLI's own exit 2
above is a different thing.

Build it and look before you claim. A run without `--track` changes nothing in
Hadron (only the reconcile pass may tell the adapter about earlier changes), so
use it as the dry run:

```sh
go build -o intake-adapter main.go          # items.json sits beside it
hadron team task find-untracked --adapter ./intake-adapter
hadron team task find-untracked --adapter ./intake-adapter --for Marco --json
```

Then claim one item as a task of your App:

```sh
hadron team task find-untracked --adapter ./intake-adapter --track --limit 1 --assignee user:<your-handle>
```

`--limit` with `--track` is how many tasks the call holds (default 1). The
claim goes to `--assignee`, else the `--for` worker, else your bound worker.
With none of these (a plain shell with no bound worker session), `--track`
exits 2 and claims nothing, so name the assignee as above. The
full flag reference is `hadron team task find-untracked --help`; the machine
contract (`--json` shapes, exit codes) is in `hadron agentic-usage`, section
"App properties, task externalId and find-untracked".

## From the sample to a real adapter

- Replace `load` with your tracker's search for open items, and page with the
  tracker's own cursor or page token.
- Return each item's canonical URL as `externalId`, the same URL every time.
- Write the mark in `mark-tracked`, and answer `untrack` (remove the mark) and
  `get`; list them in `describe`.
- Read the tracker credential from a variable of your own (not `HADRON_*`) and
  put the reason for any refusal on stderr.
