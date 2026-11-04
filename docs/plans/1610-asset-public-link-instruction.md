# Asset public-link instructions (#1610)

`asset public-link --instruction <kind>:<ref>` uses the shared instruction
parser. Parsing happens before confirmation or network access. The reference
following the first colon is forwarded unchanged. The five-key public-link
DTO and the existing refusal mapping remain the same.

Without the flag the command sends the unchanged legacy operation, without a
worker session. With it, a separate operation carries the instruction; its
minimum server is 5fd87dcca679876f474c969c121593cf67d6d5a0 (#3368).

The binding is snapshotted before confirmation and verified again after client
setup, immediately before sending. A complete unchanged binding on the chosen
server contributes its session. Otherwise the call remains the person's, using
the shared fallback diagnostic. No target metadata lookup is made. The server
resolves the asset's memory and App and owns admission, including wrong-App
refusals (Xanthos fit ruling, team chat #72650). A server refusal never causes
fallback or retry. This adds no mutation to the authored-write allowlist.

Each accepted or refused invocation sends one mutation with a five-field
selection. Request count and response size do not grow with tenant size; there
is no roster or asset-list scan. --on still confirms; --off does not.

Author verification uses fake transport, including instruction document and
variables, legacy omission, early malformed-input refusal, confirmation,
PERMISSION_DENIED/NOT_FOUND, session headers and rebind during client setup.
Independent QA must run a compiled binary on one private stack for the person,
instructed-worker and uninstructed-worker App-memory cases and refusals.
