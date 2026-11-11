# App lifecycle commands: delete / uninstall / reinstall (cli#1917), as built

## Contract
- hadron-server#3774 added `uninstallApp(ref)` / `reinstallApp(ref)` beside `deleteApp(ref)`.
- cor:dmo:050:21 names the three states:
  - **installed**
  - **uninstalled**: kept, read-only, handle freed
  - **deleted**: no longer serving, kept, no undelete
- The CLI's `app uninstall` called `deleteApp` and printed "Uninstalled", the spec's word for the other, reversible state.
- Holger chose option A (R4, recorded by Vera at https://github.com/hadron-memory/hadron-cli/issues/1917#issuecomment-6805521382): one breaking release, with `rm` moving to delete.
- Xavier's CHECKED gate (6805658203) bounds the build.

## Shape
`internal/cmd/app/lifecycle.go` replaces `uninstall.go`. One helper, `runAppLifecycle`, runs each command:
1. syntax-only canonicalization of the ref (`CanonicalAppRef`: an id stays as is; `org:slug` becomes the v2 URN; no lookup);
2. optional confirmation;
3. **exactly one** mutation;
4. `api.MapError`;
5. `appStateDTO{app,status}`.

| command | alias | confirm | mutation | status |
|---|---|---|---|---|
| `app delete` | `rm` | `ConfirmDeletion` ("cannot be undone") | `deleteApp` | `deleted` |
| `app uninstall` | none | `Confirm` (names read-only + `app reinstall`) | `uninstallApp` | `uninstalled` |
| `app reinstall` | none | none (restores the same App) | `reinstallApp` | `installed` |

Victor's four FIT conditions (6799896107), and where each is met:
1. **The spec's words:** the help, text and `status` use deleted/uninstalled/installed.
2. **The ref goes through unchanged, with no client resolution.** `app delete`'s help says to name an uninstalled App by id. The uninstall receipt suggests reinstalling by id when an id was given, and `<its id>` otherwise.
3. **No oracle:** there is no preflight read. FORBIDDEN is rendered once, and a pin asserts one request.
4. **No transition claims:** the text and JSON state the resulting state, a pin asserts a repeat prints identically, and nothing says "reinstalled" or "new".

`APP_DELETED` and `APP_URN_IN_USE` map to Conflict (5), beside `APP_UNINSTALLED`.

## Schema
The snapshot was refreshed at hadron-server `9478620f` (origin/main, contains `174fa15f`). #3957's `chatAiConfigChoices` / `setAppChatbotAiConfig` came in with it and are classified `gap #1913`, not implemented here.

## Compatibility references updated
- the coverage matrix (`deleteApp → app delete`, the pair flipped from `gap #1915`)
- `docs/help-inventory.md`
- `CLAUDE.md`
- the use-hadron-cli skill
- `agentic-usage.md` (the app surface line and the destructive list)
- the ref-form pins
- `docs/release-notes/app-lifecycle-1917.md` (BREAKING mapping)

hadron-docs `docs/reference/hadron-cli.md` follows through its own lane (linked at closeout).

Unchanged neighbours, pinned: the `app agent remove` alias `uninstall`, `team health uninstall` and `team usage uninstall`.
