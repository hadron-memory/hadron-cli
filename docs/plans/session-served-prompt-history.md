# Session served-prompt history (cli#1280, session slice)

`team session served-prompts <session-id>` reads the existing server `session(id).servedPrompts` field. The explicit id is independent of worktree/App selection; `team session list` supplies ids. The Chat slice remains held pending a server point-read and contract ruling (Ada #50440). This PR leaves #1280 open.

One request reads one history page in server order; the CLI neither sorts nor reconstructs records. `--limit`/`--offset` page records (50 default, 200 cap, zero counts). Independent `--provenance-limit`/`--provenance-offset` page every returned record's provenance, also 50/200/zero. No automatic full scan or snapshot guarantee is offered. The existing worker/run provenance paging and JSON remain unchanged.

The stable JSON envelope is `{servedPrompts: {items, total, limit, offset}}`. Items use the existing immutable record DTO including a provenance page. Empty items are `[]`. A null session remains `{servedPrompts: null}`; hidden records/serving off may be an empty page. We do not manufacture a refusal or distinguish concealed data from absent data. Actual provider errors pass through `api.MapError`. Invalid wire pages fail before any output.

The generated operation reuses the scalar-only record fragment. No schema refresh is needed: the committed schema already contains the operation's fields, verified against server main 9f42805b8349ce15c21475d49e010014f1f11611. No source bodies, live edges, recording changes, server changes or Chat workaround are included. Recorded variables may be sensitive and are only shown on this explicit read.
