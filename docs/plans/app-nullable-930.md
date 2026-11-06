# Nullable withheld App fields — cli#930

Gate Xabat6678919018 (T1), author attach6678884040, Xylon6540205767 served for CLI build; server#2099 still merge-held. Eden44340/6501003031 and Vera6455316071 set presentation. No independent client access rule. cor:dmo:050:03's withheld-install paragraph retains Proposed/part-built status.

## Compatibility source and generation

Base CLI57598221ffa7b412ca3646abbf11242557a32074. Nullable provider source: open serverPR2099 head85cc45e4c31294054de63df9bfd73b3a3b77d65c, typeDefs blob1359cd849006c5ee13cfed4b9160d7656cbbaf9a. Exactly App.members/memberCount/appAgents/agents/hasAiApiKey lose outer !; elements stay nonnull, Agent.hasAiApiKey unchanged. Current provider main07d96c78 remains nonnull.

The committed provider snapshot remains unchanged. Query response annotations request pointer:true for memberCount in Apps, CreateApp and CreateAppSettings; make generate creates nullable response values even before the provider changes. List slices already retain null versus [] in decoding. The pinned full nullable compatibility fixture from the attach (sha256c29fde10638d72aed6402ed41eedde678b804f374fe3732ab070239c9da59c51, based on schema blob819eda75316a56fac2cfd5569d613f0ff0a403d3) is used separately to verify generation compatibility, never presented as a landed snapshot. No query document or requested field changes, and no fallback/extra request.

## Independent values and output

appDTO memberCount is an explicit *int without omitempty; list and both install branches preserve null/0/N. No human count column added. App.members and App.hasAiApiKey are unselected and require no new read. The legacy first Agent link remains unrelated to completeness.

approster.Result keeps independently known install count (len appAgents), withheld-install count (null joined Agent), and list withholding (either selected list nil). Withheld details emit bare JSON null, human withheld cue, exit0. A known appAgents count survives an unavailable Agent detail list. Available partial JSON remains readable-detail rows only; text reports N installed/M not visible. Known complete/empty output keeps its previous bytes. Role consistency warnings require a complete, available roster. InstallMethod/importer and boot-pointer provider nulls remain unchanged; the client does not reconstruct authority or identities.

## Evidence / remaining checks

Red0e120e66: memberCount null vs 0/N across all three operations; independent list nulls, partial/all-hidden installation counts, role-warning suppression and complete positive control. First implementation green pass exposed old fixtures omitting selected appAgents while claiming a known roster; only those fixtures now include valid joins. No product fallback for missing fixtures.

Focused affected command tests and new strict recording fake run under -race and a 4GiB bounded scope. No full local suite slot used; hosted CI covers the full suite. Seven exact-match behavioral mutants killed the new pins (drop null key, drop each independent nil-list check, count from agents, drop hidden increment, turn nil into [], allow warnings without completeness); arithmetic +0 control survives. Source patches/logs retained, no compiler failure counted. The exact pinned nullable fixture produced byte-identical generated client bytes with the response-pointer annotations; production schema was restored unchanged. Independent compiled CLI QA remains required at freeze; tests do not claim a live-provider run. QA must compare true base/head output on known-empty/complete data, and exercise independent null/N/M/role-warning behavior through the compiled CLI. No stack, provider activation or production run is authorized by this build.

## Sequence / companions

Stop at ready; no merge/release from author. CLI tolerance must merge AND be released/available with exact SHA/tag/assets/Homebrew/install evidence before server2099 merges (auto-deploy). Portal1148's separate verified deployment remains required. Existing docs437 follows client release + provider deployment; do not publish future behavior as live. No spec mint/edit or provider change here.
