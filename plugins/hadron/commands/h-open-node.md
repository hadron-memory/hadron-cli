---
description: Find a Hadron node by name and open it in the portal (hadronmemory.com)
argument-hint: [node name or URN]
---

Find a Hadron node and open it in the browser at the Hadron portal.

0. **If `$ARGUMENTS` is empty**, ask the user which node — a name or a `hrn:node:…` URN — they want to open, and stop until they answer.
1. **Resolve `$ARGUMENTS` to one node URN:**
   - If it is already a fully-qualified URN (`hrn:node:…`, or `org::memory::loc`), use it directly.
   - Otherwise call the **`h-search`** MCP tool with `query: "$ARGUMENTS"` and `entityTypes: ["node"]`, and take the best-matching node's URN. If several match closely, show them and let the user pick one.
2. **Build the portal URL:** `https://hadronmemory.com/app/u/<urn>` — where `<urn>` is the node's fully-qualified `hrn:node:…` URN, appended **as-is** (the `/app/u/[urn]` route resolves it; no extra encoding needed).
3. **Open it** in the default browser with the platform-appropriate command: `open "<url>"` on macOS, `xdg-open "<url>"` on Linux, or `start "" "<url>"` on Windows. Also print the URL so the user can click it.

Resolve and open directly — do **not** launch a background agent.
