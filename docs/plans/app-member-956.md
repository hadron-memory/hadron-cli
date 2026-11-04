# `hadron app member add|remove|list` (cli#956) — design as built

Built from the CHECKED gate on cli#956 (T2 + scoped §10) and the Opus plan on the same issue
(written against hadron-cli main `9e5d14e3`; main had not changed any file it cites at pickup).
Spec: `cor:dmo:050:04` (AppMember), `cor:dmo:050:12` (who manages an App).

## Surface

| Command | Wraps | Notes |
| --- | --- | --- |
| `app member add --user <ref> --role <r>` | `ensureAppMember` | idempotent upsert; role required, no default |
| `app member remove --user <ref>` | `removeAppMember` | server answers `true` whether or not a row existed |
| `app member list` | `App.members` | visible rows only |

The App is the root `--app` flag, else the active App. No positional App.

## Decisions

- **`--app ""` refuses.** `Factory.App()` treats a blank flag like an absent one; `memberTargetApp`
  checks `Flag("app").Changed` first. The resolved ref (and which branch supplied it) is frozen once and
  used for the prompt, the mutation and the receipt.
- **`--user` is passed through verbatim** (id / email / handle / `hrn:user:`): no trim, no case fold,
  no pre-resolution, no `list` call to "detect" a role change. Consequently every add and remove is
  confirmed; `--yes` skips, non-TTY without it exits 2 with zero requests.
- **Remove uses `Confirm`, not `ConfirmDeletion`** (review:confirm-prompt-tells-the-truth): the change is
  reversible, so "cannot be undone" would be false. *Deviation from the plan, which named ConfirmDeletion.*
- **Definite vs unknown outcome** (`api.AppMemberWriteRefused`). The server re-reads the row after
  commit, so a masked INTERNAL error can follow a committed upsert. Only `InvalidRoleError`,
  `MaxMembersExceededError`, `OrphanAppError`, NOT_FOUND, UNAUTHENTICATED, BAD_USER_INPUT, FORBIDDEN or a
  `Forbidden:` message are definite. Anything else (transport, `data`+`errors`, empty payload) exits 7
  "outcome unknown", prints no DTO, is never retried, and points at `app member list`.
- **Exit codes** for the typed refusals were added to `codeForExtension`: `InvalidRoleError` 2,
  `MaxMembersExceededError`/`OrphanAppError` 5.
- **Remove reports `status: "absent"`**, never "removed". A non-manager's refusal for any foreign target
  (including an unknown one) is printed as the server wrote it — no "user not found" translation.
- **List** selects `role createdAt user { id handle urn }` only (name/email are withheld by the server).
  Empty means "no members visible to you"; `app: null` is NotFound; a null row is an error.

## Residual (not in this cut)

R113: `callerManagesAppMembers` has no worker-session refusal and `internal/cmd/app` sends no session
header, so a bound worktree's `app member add` acts with its holder's authority (as `app key` does today).
Server convergence onto `managesApp` and a CLI follow-up attaching the session header are separate issues.
The server also has no last-owner guard on role changes/removal; the confirm-every-write rule is the
CLI's only mitigation.
