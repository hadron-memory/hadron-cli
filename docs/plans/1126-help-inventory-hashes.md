# Help inventory assessment hashes (#1126)

Built from main `65c07c21`. This is repo-local generator bookkeeping; no command help, public JSON, platform rule or CI requirement changes.

The old gate was re-verified before building. Closed #2310 intentionally removed commit/version pins and their failure path. Wolfgang’s [amended acceptance](https://github.com/hadron-memory/hadron-cli/issues/1126#issuecomment-6975718146) retires that obsolete negative case and keeps generation independent of `--version`.

## Row format and migration

`path | aliases | generated | has-examples | child | help-hash | evaluator | result | finding`

The generator owns `help-hash`: the first 12 lowercase hex characters of SHA256 of the help captured for that page, including stderr and excluding the trailing newlines Bash command substitution removes. It uses `sha256sum`, or `shasum -a 256` when the former is unavailable. It never reads the binary’s version. The three human columns remain last. Whitespace padding around a stored hash is ignored, so Markdown alignment does not invalidate an assessment.

| Existing row | Refresh result |
| --- | --- |
| Same help hash | Keep evaluator, result and finding |
| Changed nonempty hash | Clear evaluator and finding; result becomes `unread`, for both prior `pass` and `fail` |
| Legacy eight-column row or empty hash | Seed the hash from current help; keep human columns |
| New command page | New hash, empty evaluator/finding, `unread` |
| Removed command page | Row omitted |

First adoption deliberately cannot detect changes between an old assessment and this migration. That is the approved seed-without-reset choice, not evidence that historical help was unchanged. At the starting main, all 693 rows were seeded: 291 pass, 18 fail and 384 unread, with every prior human cell preserved. A second complete generation is byte-identical. Later help fixes reset their rows and require reassessment.

## Failure and publication paths

A nested or root help failure, failed digest operation, missing digest tool or malformed existing row stops generation before the header is published. The existing `write-if-produced.sh` stages beside the destination and publishes only after successful, nonempty generation; its behavior is unchanged. Integration fixtures verify root/nested help failures leave destination bytes and mode untouched and remove staging files. Commit/version parsing is not a failure path after #2310.

## Verification

The first commit `042bf511` adds red-first reset/seeding/hash tests; failure and no-version-dependency fixtures already pass on main. Tests cover changed pass and fail, unchanged assessments, both legacy/empty-hash adoption, exact captured-text hashes, idempotence, root/nested failures, the wrapper, no version reads and the shasum fallback. Existing structure and large-page SIGPIPE tests retain their checks with the new column.

Private-GOCACHE mutations kill disabled reset, hash-padding normalization, seed-as-reset, swallowed help failure, short digest and restored version dependency. Removing only `|| exit 1` is equivalent because `set -e` still propagates failure; disabling `help_of`’s failure return is the semantic negative control. A commuted comparison is another surviving equivalent control. Source hashes, patches and logs are retained in author evidence.

No help audit assessment is re-authored here; the rubric and child audits remain unchanged. `make help-inventory-check` remains an explicit tooling check, not a new per-PR CI gate. The T1 schedule still requires independent LOCAL, BLIND and QA, including first adoption on a copy of the real inventory.
