# Session served-prompt history (cli#1280, session slice)

`team session served-prompts <session-id>` reads the existing server `session(id).servedPrompts` field. The explicit id is independent of worktree/App selection; `team session list` supplies ids. The session slice shipped in cli#1293. The chat slice is now `chat served-prompts <chat-id>`, consuming the record-only query from server#2865 (6b3f1e15), as ruled after Ada #50440. This is a chatbot chat id, not a team-chat Channel or message-parent node URN.

One request reads one history page in server order; the CLI neither sorts nor reconstructs records. `--limit`/`--offset` page records (50 default, 200 cap, zero counts). Independent `--provenance-limit`/`--provenance-offset` page every returned record's provenance, also 50/200/zero. No automatic full scan or snapshot guarantee is offered. The existing worker/run provenance paging and JSON remain unchanged.

The stable JSON envelope is `{servedPrompts: {items, total, limit, offset}}`. Items use the existing immutable record DTO including a provenance page. Empty items are `[]`. A null session remains `{servedPrompts: null}`; hidden records/serving off may be an empty page. We do not manufacture a refusal or distinguish concealed data from absent data. Actual provider errors pass through `api.MapError`. Invalid wire pages fail before any output.

For the shipped session slice, the generated operation reuses the scalar-only record fragment. No schema refresh was needed: the committed schema already contains the operation's fields, verified against server main 9f42805b8349ce15c21475d49e010014f1f11611. No source bodies, live edges, recording changes, server changes or Chat workaround are included. Recorded variables may be sensitive and are only shown on this explicit read.

## Chat slice

`ChatServedPrompts` calls only `chatServedPrompts(chatId, limit, offset)`, reusing the immutable record fragment and existing DTO/rendering conventions. It does not read `myChats` or a Chat object. The id is explicit; chat config, ambient App and worker binding never choose it. The server alone gates the first-record App and filters that App's records. No-App records retain the provider's platform-admin-only/non-impersonation rule.

A valid empty page is `{servedPrompts: {items: [], total: 0, limit, offset}}`, exit 0, for unknown/denied/no-record/serving-off alike; actual provider errors are mapped. A null response/page or malformed records fail before any output. Both history and per-record provenance page independently, including count-only zero limits. Text is terminal-safe and JSON preserves recorded variables; variables may be sensitive. This slice intentionally does not refactor the already-shipped session command.

The schema snapshot is exported from explicitly selected server `6b3f1e15c85c8a1d9584925e72dfd6e2b47ab336`, not an arbitrary sibling checkout. The snapshot also includes main's APP_MEMORY source kind and chat duplicate-post documentation; no unrelated operation is added by hand. Production usage needs a deployment containing that server revision; no CLI release or server deploy is part of this PR.
