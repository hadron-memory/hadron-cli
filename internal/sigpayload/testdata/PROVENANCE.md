# Provenance of signaturePayload.v3.fixture.json

This is a byte-for-byte copy of `src/lib/signaturePayload.v3.fixture.draft.json`
at hadron-server `0a6a226f77947d99c13b94381b661054f87ce471` (shipped in
hadron-server #3120).

The file's own `description` still opens "DRAFT … not law until Vera reviews and
Holger approves". That sentence is stale. The bytes are approved and merged:
Holger's R112 approved them conditionally on case 3, and Vera's re-check
(hadron-server #2104, comment 6300420767) confirmed the canonical-id fix. The
`draft` in the upstream filename does not make them unapproved (cli#879 gate,
Xia). The file is left unedited so it stays identical to the server's; a test
pins its SHA-256.
