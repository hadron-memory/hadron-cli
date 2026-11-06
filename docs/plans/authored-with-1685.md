# Explicit authoring attestation (slice B)

The client sends an explicit task/revision claim; the server judges it.
This is attested authorship, not verified compliance. No rule or citation is
minted, and nothing claims the task was read or followed. Task inference in a
future scaffold slice remains outside this change.

## Source and compatibility

CHECKED T2: cli#1685 comments 6679301479 and amended pin 6679466990.
Provider: hadron-server 07d96c78eb29eb6e3e36b2ea2ac9e6f165214dbf, containing
#3489 merge f9db35fe and the orgDefault schema already required by CLI main.
The earlier exact merge export failed generation because it predated that
field; it was restored rather than hand-edited. The amended export adds only
specDocument's unused SDL surface; generation was initially unchanged.

Explicit-only operations add authoredWith variables/arguments:
UpdateNodeDataAuthored, UpdateNodePropertiesAuthored and four typed
Update*ClearingRoleAuthored variants. Ordinary data/properties/role-clear
documents remain byte-identical, and a nil claim selects them; no new
argument reaches an older server.
All existing claim-bearing input structs already omit authoredWith when nil.
An old server lacking those arguments cannot execute the explicit variants;
the required provider revision is named above. There is no fallback write or
request that could change the meaning of the user's claim.

## Call-site inventory

| Command | Typed write | Claim propagation |
|---|---|---|
| node create (alias add) | CreateNodeInput | explicit context claim |
| node update, including data replacement | UpdateNodeInput | explicit context claim |
| node update --role "" | four typed clearing-role arguments | explicit-only document, ordinary document without a claim |
| node update --data-merge | updateNodeData argument | explicit context claim |
| node set-properties | updateNodeProperties argument | explicit context claim |
| node merge | MergeNodesInput | explicit context claim |
| replace text | SearchReplaceInNodesInput | preview and apply |
| spec new | four CreateNodeInput constructors | ordinary create, co-contract, exact positional loc, scaffold chain |
| spec edit | UpdateNodeInput | guarded proposal write |
| spec replace | SpecSearchReplaceInput or SearchReplaceInNodesInput | preview and apply on both routes |
| spec supersede | CreateNodeInput + UpdateNodeInput | successor and retirement |
| spec extract | CreateNodeInput + UpdateNodeInput | extraction and optional source trim |
| node import | existing bundle claim | unchanged, no override flags |

The issue's labels “node setproperties” and “node replace” refer to the
existing commands node set-properties and replace text; no spelling or alias
is added. Edge-only writes made by supersede do not accept authoredWith in the
pinned SDL and are unchanged. The same explicit claim goes on every node
write accepting it; no client-side role matching or new inference is added.
Other commands (including object writes and team-role writes) do not gain
flags. They can still show a server's refusal and load route, with no retry
sentence promising unsupported flags. No other SDL claim-bearing input is
left out: ImportNodeInput retains its existing bundle forwarding.

## Implementation and output

At root assembly, the ten covered executable commands receive both flags and
a RunE wrapper. The wrapper validates pairing before invoking any command
read, then stores only the explicit claim in that invocation's context.
Nested scaffolds share it; the context is restored afterwards. Every typed
constructor/argument copies that value. Nil omits the key, preserving writes
that make no claim. Pair validation uses flag presence, not zero-value guesses.

Only four typed authoring codes get curated output. An exact taskAccess READABLE discriminator carries
nullable task/taskId/taskRevision and the complete three-field load object.
Human output names the required task and revision and the available CLI read.
Retry flags are offered only by a covered command. Controls are made safe for
the terminal, and unsafe task text is not turned into a runnable retry line.

Anything except exact READABLE is concealed, including inconsistent or missing
access metadata: no task identity, revision or load survives in JSON or human
text. MapError also conceals the message, protecting partial-write warning
paths; root rendering enforces concealment even for an unmapped error. The
original error chain remains intact for exit mapping. Other codes keep their
existing output and exit behavior.

## Validation boundaries

Red-first commit 87e2cac9 contains the initial behavioral pins and amended SDL.
Synthetic transport tests exercise explicit/absent claims, pairing before any
request, all node-write constructors and multi-write spec flows, all four
codes in human/JSON through flag-bearing and flag-less commands, and deliberately
inconsistent UNAVAILABLE fields. Focused race regressions cover the affected
node/spec/replace paths. Full suite runs in hosted CI; no full local-suite slot
is assumed. Independent LOCAL/BLIND/security and compiled CLI/private-stack
QA at the named provider remain required before readiness. No production,
release, deployment or new attestation rule is claimed by these tests.

Other constructors explicitly outside the stamped table: coding preflight
create/setup (two sites), coding review create, spec rescind's lifecycle
update, team session shed's handoff create, and node import's structural
create/update/tree helpers. They are separate authoring/lifecycle workflows
and gain no override flags in this slice; no task is inferred for them.
Kind-specific createTaskNode/createReviewNode/updateTaskNode/updateReviewNode
routing reached by the covered generic node commands retains the same input
claim through the existing shared authoring helpers.


QA applicability was confirmed by Xan (team chat #83634) and Eden (#83637):
one fresh private stack at 07d96c78 drives four real codes through a
flag-bearing write and MISSING/BROKEN through a flagless write. A flagless
write cannot supply a wrong or stale claim, so those two combinations are
unreachable on the real provider; the synthetic four-by-two human/JSON matrix
covers their rendering. This is not a fake-only substitution for stack QA.
Supersede retains an attestation cause across partial retirement errors;
extract keeps its existing best-effort trim exit and adds recovery to its
warning. Large integer revisions remain decimal in retry hints.
