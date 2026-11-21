# `app install` and `app update` no longer accept `--role`

Unreleased (cli#2515, stage 2 of the ruled removal of the App's own role; stage 1
is hadron-server#4469, merged). **BREAKING for scripts that pass the flag.**

The App's own role never had a defined effect, and it was ruled removed
(cli#2497). The server still accepts a `role` on `createApp` and `updateApp` and
ignores it for a notice period (no earlier than 2026-12-21); the CLI no longer
sends it.

| Before | Now |
|---|---|
| `hadron app install ... --role READER` | `--role` is refused (exit 2) before any request: "--role was retired: the App's own role setting was removed ..." |
| `hadron app update <app> --role READER` | the same refusal, exit 2, nothing sent |

- **What to change:** delete `--role` from the command. Nothing replaces it.
- **Not affected:** a *member's* role (`hadron app member add --role`, `hadron app
  member roles`) and the Agent memory access levels.
- **Help:** `app install --help` and `app update --help` no longer list `--role`.
- **Requests:** `CreateAppSettings`, `UpdateAppSettings` and their policy variants
  no longer declare or send `$role`.
