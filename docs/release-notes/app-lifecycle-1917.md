# `app uninstall` no longer deletes: `app delete`, `app uninstall`, `app reinstall`

Unreleased (cli#1917, over hadron-server#3774 / spec cor:dmo:050:21; Holger's
option A, R4). **BREAKING.**

| Before | Now | Server call | Resulting state |
|---|---|---|---|
| `hadron app uninstall <app> --yes` | `hadron app delete <app> --yes` | `deleteApp` | deleted: no longer serving, kept, **no undelete** |
| `hadron app rm <app> --yes` | `hadron app rm <app> --yes` (unchanged: `rm` moved with delete) | `deleteApp` | deleted |
| (none) | `hadron app uninstall <app> --yes` | `uninstallApp` | uninstalled: kept, **read-only** (writes refused `APP_UNINSTALLED`), handle freed |
| (none) | `hadron app reinstall <app>` | `reinstallApp` | installed: the **same** App (id, keys, members, memories) |

**Scripts that ran `app uninstall` to delete an App now uninstall it instead.**
That is the reversible, less destructive outcome, and the output says so. To
keep deleting, change the command to `app delete` (or `app rm`). `app uninstall`
no longer has the `rm` alias.

- **`--json`** is `{"app": "<ref>", "status": "deleted" | "uninstalled" | "installed"}`.
  `status` is the resulting state. For `uninstall` and `reinstall` the server
  answers a repeat as it does a real change, so a repeat prints the same thing.
  A repeat `app delete` is refused: `APP_DELETED` (exit 5) by id, and the
  identical refusal (exit 8) by URN, which a deleted App no longer holds. The old
  `"status": "uninstalled"` for a deletion is now `"deleted"`.
- **Refusals:**
  - `APP_DELETED` (exit 5): a deleted App can't be uninstalled, reinstalled or deleted again.
  - `APP_URN_IN_USE` (exit 5): another installed App holds the handle, so reinstall is refused.
  - A missing App and a caller without the right get one identical refusal (exit 8), for all three commands.
  - The `--json` error envelope carries the server's code as `serverCode`.
- **Refs:**
  - An id or URN is sent to the server unchanged.
  - A URN names the installed App holding it, else the one uninstalled App with it.
  - Name an uninstalled App by id to delete or reinstall it: its former URN may now name another App.
- **Confirmation:** `app delete` and `app uninstall` both ask on a terminal and need `--yes` otherwise. `app reinstall` doesn't ask.
