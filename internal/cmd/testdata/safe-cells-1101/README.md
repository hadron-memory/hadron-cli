# Team table compatibility snapshots

Captured by `TestTeamSafeCells1101` against main `5face16efd96141a1edd8ee805142cff3d3bfbf5`, before applying the rendering patch. Each file contains SHA-256 hashes of clean text and attacked JSON output. The attack is the issue's `a\nb\x1b[31mc` label followed by 1,000 `界` runes.

The preview text includes the synthetic HTTP server's ephemeral port; that port alone is replaced with `PORT`. The team-filtered task list computes `ageSeconds` and its IDLE text from the wall clock; those two clock values alone are normalized. Every other byte, including JSON indentation, escaping, nulls, keys, values, and array order, participates in the hashes.

To recapture intentionally, run the test against the baseline renderer with `UPDATE_SAFE_CELLS_1101=1`. Do not regenerate these snapshots with the changed renderer to make a compatibility failure disappear.
