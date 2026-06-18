---
description: Run a Hadron task (a runnable memory node) by name via the h-run-task MCP tool
argument-hint: [task name] — optional; omit to choose from a list
---

Run a Hadron task by invoking the **`h-run-task`** MCP tool **directly in this session**.

- Call `h-run-task` with `task: "$ARGUMENTS"`. It fuzzy-matches that against the runnable nodes (`isRunnable = true`) in the Hadron memories you can read, and runs the match.
- If `$ARGUMENTS` also names a memory (e.g. `review-changes in mmdata`), pass the memory part as the `memory` argument and the task part as `task`.
- If `$ARGUMENTS` is empty, call `h-run-task` with **no** `task` so it lists the runnable tasks for the user to pick.
- If `h-run-task` returns a list of candidates (ambiguous match, or no hint), show them and let the user choose, then call `h-run-task` again with the chosen `urn`.

Do **not** launch a background agent or use the Task tool, and do **not** use `h-find-nodes` / `h-read-node` to "find" the task first — `h-run-task` does the resolution itself. Just call `h-run-task`.
