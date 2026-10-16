# Comment anchor hash compatibility change

Comment JSON output now emits `anchorApprovalHash` as a string or `null`, instead
of always a string. A comment on an encrypted target has no anchor hash and now
returns `"anchorApprovalHash": null` rather than `"anchorApprovalHash": ""`.
Consumers must accept null; the key remains present. Plaintext hashes are
preserved byte-for-byte. This applies to get, list (roots and replies), and write
receipts. Anchor revision, currentness, nullable body/stub fields and exit codes
are unchanged. Human-readable output still shows the anchor revision and
currentness without a fingerprint.
