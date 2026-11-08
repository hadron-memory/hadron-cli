# Sync reports can use version 2

Unreleased (cli#1816; implemented in cli#1815): `hadron memory get --json`
and `hadron memory sync pull --json` can return `syncReport.version: 2`
alongside version 1. Scripts reading recorded reports should handle both
versions. Version 2 adds `attestation_refused` entries with the codes
`AUTHORING_ATTESTATION_MISSING`, `AUTHORING_ATTESTATION_WRONG_TASK`,
`AUTHORING_ATTESTATION_STALE`, and `AUTHOR_TASK_BROKEN`.

The allowed kinds and codes remain closed for each version. An unknown
version, kind, code, or kind/code pairing makes the whole report unavailable;
it does not expose a partially accepted report. Existing exit behavior is
unchanged. This note records CLI acceptance, not a claim that a deployed
server emits version 2, and does not publish a release.
