# AI-config management (#1347)

The server already owns the permission, egress and metering rules. This CLI
change reaches its uniform masked list, saved-config test and API-style inputs.
The committed schema at CLI base c32c3bbe includes all required fields; no
server export or schema change is needed.

`ai-config list` keeps its existing enabled/resolved chat-context mode. Explicit
`--owner-type` plus `--owner-id` selects `aiServiceConfigs`; `--all-owners`
selects the server's cross-owner view. Global App context never becomes an
owner filter. The new mode returns one `{items,total}` page, including disabled
configs, with explicit limit/offset and a count-only limit0. It selects only
masked fields, stored/effective endpoints and stored/effective API styles.

`ai-config test <id>` performs one `testAiServiceConfig` mutation. Optional
`--override-file` supplies edited provider/model/key/endpoint/style/params
without saving them; `-` is piped JSON, protected by the document-at-TTY rule.
A bound input type uses omitempty RawMessage fields to preserve omission,
explicit null and supplied values. File shape/types are checked without echoing
content; provider policies remain on the server. The output is an explicit DTO
with effective values, sources, knobs and the server-scrubbed diagnostic. A
completed test exits0 even for `ok:false`; request refusals use api.MapError.
Only provider-reaching tests make the small metered real call; NO_API_KEY and
KEY_UNREADABLE are no-provider-call results per the server contract.

`create --api-style` and file `apiStyle` use a dedicated typed operation.
`update --api-style` sets the enum; `--clear-api-style` uses a dedicated
operation with literal null. Other optional inputs still omit when absent.
Style mutations are separate from old operations so existing invocations keep
their original selections and output. Combining endpoint edits with style edits
uses one mutation. No provider authorization is inferred locally.

Validation starts with a red-only test commit for the four matrix gaps.
Command tests capture variables, omission/null, masked DTOs and refusal paths;
compiled-binary QA and distinct cross-family LOCAL/BLIND plus scoped security
review are requested under the CHECKED T2+section10 schedule. Local checks do
not claim live provider metering or egress behavior from a fake server.
