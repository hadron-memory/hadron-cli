# AI config endpoint CLI (#739)

## Contract pinned for implementation

The schema snapshot was exported from hadron-server merge commit
`9c4c18bd22aea01427d6c60b5e975d3d1959e0dd` (server PRs #1380, #1385,
#1389 merged). The server owns endpoint validation, default resolution, and
`aiProviderEndpoints(provider)` suggestions. Its update resolver treats an
omitted endpoint as preserve, and `null` or the empty string as clear. Its
`effectiveEndpoint` field reports the chosen URL; bedrock may return null
because its host derives from the region.

## CLI shape

- `ai-config create --endpoint <url>` and the `endpoint` key in `--file` select
  the endpoint-aware mutation. The flag takes precedence over the file.
- `ai-config update <id> --endpoint <url>` replaces the override;
  `--endpoint ""` clears it. An omitted endpoint stays omitted on the wire.
  The empty string is used as the clear signal because genqlient's optional
  pointer omission cannot represent both absent and explicit JSON null.
- `ai-config get <id>` and `ai-config list --with-endpoints` show the stored
  override and server-resolved effective URL as distinct fields and columns.
  The latter is opt-in so the existing picker query remains valid on older
  servers. JSON includes both nullable fields when endpoint support was used.
- `ai-config endpoints <provider>` renders the server's URL, label, default
  marker, and note without a client-maintained endpoint catalog.
- Original create/update/list operations keep their old selections. Explicit
  endpoint operations fail with an upgrade message on a GraphQL validation
  refusal. Server typed AI-config errors retain their messages and map to the
  CLI's usage/not-found/conflict exit codes.

No provider call or production config change is part of this CLI feature.
