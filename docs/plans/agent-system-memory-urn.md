# Agent detail system memory URN (cli#1724)

Client-only best-effort display, Refs #1724 / #1656 row 5. No server field or access rule is added. Xan’s CHECKED gate requires T2 LOCAL, FULL BLIND, scoped security at each frozen head and compiled CLI QA; UX waived.

`agent get` keeps its existing GetAgent operation. When its systemMemoryId is nonempty, the same credential/client performs one targeted `AgentSystemMemory` read selecting only memory id and urn. No clone-source lookup or listing enrichment is added. Readable answers matching that id add `systemMemoryUrn` (optional JSON string) and text `system memory: <urn> (<id>)`. The returned URN is used verbatim, never inferred from agent organization/name. Human output uses SafeCell; JSON preserves the server string.

Every lookup error, partial data with errors, null memory, mismatched id or empty urn retains the exact id-only output with no warning. No retry, alternate access path or new refusal. No system memory id means no extra request. Agent-not-found still has its existing error. This does not assert existence or nonexistence of an unavailable memory. Existing server access gates own the answer.

Cost: one existing agent read plus at most one by-id memory read, independent of tenant size. The extra response contains two short strings (roughly 100–200 bytes for ordinary IDs/URNs), not memory contents or settings. This is an estimate; compiled QA records actual request/body/time measurements. No deployment or earliest-server floor is claimed: an unsupported optional query degrades silently. GetAgent is unchanged, so this cannot add a new field requirement to the agent read.

Validation: test-only red 27fd24dc at base356ed5cd fails because the optional read/display is absent. Two expected-output literal corrections after that red account for the pre-existing em dash and pretty JSON spacing; the original red fails request-count assertions before either literal is used. Fakes exercise text/JSON readable, unreadable, missing, partial-error, old-provider, malformed, 503, wrong-id and empty-URN cases, with exact fallback output and empty stderr. These demonstrate wire/output behavior, not a real ACL matrix. Independent scoped security and compiled QA remain required.

No spec edit: display enrichment stays within the CLI and implements the existing memory-read gate (per CHECKED). Server Agent.systemMemoryUrn, agent list, boot prompt and #1655 are out of scope. No release/install/deploy action.

Author guard probes: ignore lookup errors, ignore returned-id equality, and accept empty URNs each produce the named command-test assertion failure. Each replacement was exactly one match and printed for inspection; a parentheses-only equivalent remains green. All source restored. Focused race, lint and matrix checks pass; final full-suite and hosted CI results are recorded on the PR.
