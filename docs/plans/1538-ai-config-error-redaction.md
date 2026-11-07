# Submitted AI override keys in errors

cli#1538 adds command-local defence in depth for older servers that echo an
AI test override key in input-coercion diagnostics. Current server-side
redaction remains authoritative; no spec line or provider policy changes.

After loading and validating --override-file (file or pipe), the command knows
the optional submitted apiKey. Saved keys never reach this code. The request
and its generated operation are unchanged. Error classification runs first;
then literal and JSON/Go string-escaped echoes of that key are replaced in the
mapped diagnostic. Empty/null/omitted keys do nothing. A placeholder that itself
contains a very short submitted key is replaced by an empty string instead.

When a diagnostic actually contains a secret, the returned error preserves the
numeric exit class but deliberately drops the original error chain. Keeping
that chain would let root JSON/detail/human renderers recover unsanitized
metadata. Non-echoing errors are returned unchanged, including their chain.
Completed provider diagnostics also scrub their error code and message while
retaining ok/transient, exit 0 and the existing DTO shape. Other successful
provider payload fields are outside this bounded error-redaction change.

Red commit 3a03bc3e reaches real root rendering for text and JSON and fails on
literal/escaped echoes for exits 2/4/8 and completed diagnostic errors. It pins
the unchanged submitted wire value and retained safe guidance. A key equal to
FORBIDDEN demonstrates classification before redaction. Existing AI config
controls retain omitted/null and saved-value behavior. No schema or request
projection change, additional read, provider retry or secret persistence.

This is bounded exact-value/string-escape redaction, not a claim to remove
arbitrary transformations or fragments of a credential. Tests use only
synthetic keys and local fake servers; no real key/provider or production call.
Review is T2: distinct cross-author-family LOCAL/FULL BLIND plus scoped §10 at
every head, and compiled CLI fake-old-server QA. Release is separate.

Mutation checks (landed, compiling, then restored): bypassing request-error
redaction, bypassing completed-error message redaction, and dropping escaped
spellings each fail the intended disclosure assertions. HTTP 200 and 400
envelopes preserve exits 2/4/8 and one request; safe guidance remains.
