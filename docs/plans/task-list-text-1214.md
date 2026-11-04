# Task title and description filter (#1214)

`team task list --text <words>` passes the exact flag value to `agentTasks.text` through the separate `AgentTaskListText` operation. The omitted path keeps the original `AgentTaskList` document, including compatibility with servers predating the new argument. A changed empty flag is an explicit empty string, never omission; the server trims, validates 1–200 characters and performs literal case-insensitive substring matching. Refusals keep BAD_USER_INPUT and exit 2. With no flag the variable is omitted and DTO/output rendering is unchanged.

The coordinator view needs the full unfiltered ancestry to discover descendants whose parties are outside the team. With `--team --text`, it reads that ancestry as before, first pages the server's text-filtered tasks (refusing before the full ancestry scan on invalid text), and intersects matching IDs after the descendant join and alongside status/staleness selection. Both reads keep the existing bounded/non-advancing-page checks and fail without rendering on errors. Omission adds no read. These reads are not a transactional snapshot: concurrent edits can move results between them.

Schema source: hadron-server `7ff3f2f12292392ebd48411acf6e1019d4280ae4` (merged main; includes #2778 / 3ed8383a). The refresh also introduces the nullable darkAlarmWindowSeconds input from #2755; its omitempty annotation and a serialization guard preserve unrelated settings writes. No dark-alarm command added here.

Red-first commit 7127ade pins forwarding, omission/output, combined status/tag, server-owned empty/blank/201-character refusal, literal special characters, and an outside-party matching descendant under a nonmatching team ancestor. Existing task table snapshots cover unchanged omitted human output. T1 seats: cross-family LOCAL/BLIND and independent real-binary QA against a fake API. No real stack required.

Fix evidence: the early-refusal pin failed at a7cee426 because it saw an unfiltered ancestry request before BAD_USER_INPUT. Added successful filtered paging, later-page failure and non-advancing filtered-page pins, plus status-independent text exclusion.
