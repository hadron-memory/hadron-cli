---
description: Search the Hadron knowledge graph (nodes, memories, agents, apps, …) via the h-search MCP tool
argument-hint: [search text] — a name, URN, or keywords
---

Search Hadron by invoking the **`h-search`** MCP tool **directly**.

- Call `h-search` with `query: "$ARGUMENTS"`. It runs a global, cross-entity search (organizations, memories, nodes, agents, apps, AI service configs, users) and returns one ranked, flat list.
- Present the results clearly — name, entity type, and URN. If the user is clearly after one specific thing, surface the top hit and offer to open or act on it.

Call `h-search` directly — do **not** launch a background agent. (Use `h-find-nodes` instead only if the user asks to scope to a single memory or wants vector/semantic node search.)
