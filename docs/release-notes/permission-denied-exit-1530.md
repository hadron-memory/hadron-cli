# Permission refusals now use exit 8

Unreleased (cli#1530): the shared GraphQL error mapper now classifies
`PERMISSION_DENIED` as Forbidden (exit 8), previously generic Error (exit 1).
Scripts matching exit 1 for that specific code should handle exit 8 instead.
This is shared across commands, including `app asset-upload enable/disable`,
asset upload/finalization/reference creation, agent asset lists, and legacy
`asset rm` / `asset restore`. Public-link already mapped this refusal to 8.
Other codes are unchanged: concealing `NOT_FOUND` is exit 4 and
`INTERNAL_SERVER_ERROR` is exit 1 with its requestId. No retry is added.

## Named source audit (not a live provider run)

At server d0b374cd8accbc9c47cb0dbd3f6162fabc7a71c2, production literal emitters
are assetMemoryWriteGate, beginAssetUpload, completeAssetUpload,
createAssetReferenceNode, listAgentAssets, restoreAsset,
setAgentAssetUploadEnabled, softDeleteAsset and lib/asset/publicLink.
They are write-access/owner/org/admin refusals, not missing authentication.
The asset mutation resolver reaches beginAssetUpload, beginAssetUploadV2,
completeAssetUpload, createAssetReferenceNode, setAgentAssetUploadEnabled,
setAssetPublicLink, softDeleteAsset and restoreAsset; agentAssets uses the
listAgentAssets controller. Shared helper reach is included, not treated as
one command only. No other production quoted-code emitter was found in src.
This audit does not establish future-provider codes or deployed behavior.

Server#2979 (dadc314a) registered SetAssetEnabledError in formatError;
its wire regression pins readable/non-admin PERMISSION_DENIED, identical
unreadable/missing NOT_FOUND, and lookup faults. No schema shape changed.
The CLI snapshot already selects the operation; no refresh is needed.

CLI source sweep finds the public-link wrapper (already exit 8) and shared
mapper; old asset rm/restore generic-exit tests were intentionally updated.
Full tests pass. The stale app-toggle help, usage and old plan were corrected;
the duplicated trailing usage paragraph was removed. Release publication
is separate; this note does not assign a version or publish anything.
