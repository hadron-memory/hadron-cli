# Bulk template apply (#1701), design as built

Status: implementation before review. Gate6661431166 T1; spec cor:api:290:01 Proposed, Vera COMPLIANT6661333751. No new platform rule.

One ref without a file retains the original single operation, confirm text, DTO and exit behavior. Other calls use a distinct typed bulk mutation, so ordinary documents never select the new field. Ref arguments precede trimmed non-comment file lines (- consumes stdin once); CanonicalMemoryRef is applied individually, duplicates and order retained. No cap, chunk or automatic retry is introduced.

Bulk preview is one dryRun=true call. --dry-run prints it. Otherwise successful per-entry revisions must agree; a mismatch refuses exit5 before apply. All-failed preview prints and exits1 without a real call. Replacement roles are aggregated into one confirmation. Real apply sends the preview revision, never retries. Reports preserve server echoes in JSON and original refs in text, validate positions/shape/counts, and print all per-entry failures before silent exit1. Server text uses SafeCell; unknown error codes are rendered generically. Skips are success.

Whole-call failures use existing MapError plus caller-input exit2 for BULK_TARGETS_TOO_MANY; the provider message names cap/count. The server owns access, cap and deduplication. A partial result is not rolled back; retry failed targets only, not the whole file. A transport/protocol failure after apply may leave writes: verify configs before retrying, no automatic compensation.

Schema refreshed from owned detached server1859c3b4bad7d854bf000f19e7ba62b12f1d094f (contains573f3cf4), generated via make schema/generate. Shared fragment reuses the existing single-apply DTO conversion; no generated code hand edits.

Request cost: one mutation for --dry-run or all-failed preview, otherwise two; no per-target client fanout. A realistic50-ref call carries50 copies of the existing per-rule report, so response size grows with rules/outcomes; approximate small two-rule reports are tens of KB, not a benchmark. Provider QA must record actual request count/GraphQL response bytes/wall time and source SHA for the bounded three-ref and51-ref legs.

Evidence: initial red-first6c7bf128 (bulk args/file rejected on base), additional refusal red10dd96f2 (cap was generic1). Focused tests cover wire order/duplicate/canonical refs, preview revision, no-ok path, confirmation, file/stdin, generic mixed errors/text safety and whole-call exits. Additional meaningful malformed-report and guard mutants recorded in author review. Hosted CI supplies full-suite coverage; no full local suite without a coordinator slot.

Limits before freeze: provider leg belongs to independent QA; no live/production test, release/install/deploy. No client access check or credential handling added, no T2 scope expansion. Stop at ready; Refs1701 leaves issue open for evidence close and Vera Built propagation.
