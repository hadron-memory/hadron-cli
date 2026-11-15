# S3 CLI controls

hadron-cli#2067 exposes the controls from hadron-server#4242. It consumes
the server's S3 contract; authorization, lifecycle decisions, event emission
and repeat-call semantics remain server-owned. This CLI adds no new platform
rule or spec citation. S5 owns the Proposed lifecycle/sweep spec wording.

## Commands and output

- `agent create --name <n> --lifecycle LONG_LIVED|SHORT_LIVED`: optional
  creation-time selection, omitted for the server default LONG_LIVED. Added
  to C9 by the coordinator; #2071 is superseded by #2067.
- `agent update <ref> --lifecycle LONG_LIVED|SHORT_LIVED`: enum checked before
  any request; omitted is absent on the wire, preserving the stored value.
  Only the flag path uses the S3 mutation document. Legacy create/update
  documents and shared `AgentFields` are unchanged. `agent get` makes a
  separate lifecycle read: missing-field validation means text
  `unknown (server before #4242)` and JSON `lifecycle: null`; other failures
  fail rather than claiming an old server. Lists and unrelated DTOs stay
  unchanged; lifecycle write receipts include the returned value.
- `team worker leave-alone <worker> [--on|--off] [--reason text]`: no switches
  reads the marker; a write requires exactly one
  true switch, reason only when marking. The server resolves the worker;
  names carry App scope, direct ids/URNs do not inherit ambient App scope.
  The receipt is `{workerId,leaveAlone,reason,by,at}`; nullable fields stay
  null. Existing worker get/list documents, DTOs and tables stay unchanged;
  only the new command requests marker fields. Reason/by may be masked.
- `team app sweep-mode [OFF|OBSERVE|APPLY]`: one read when no argument is
  supplied, one mutation otherwise. Receipt: `{appRef,sweepMode}`.

Both controls accept optional `--instruction kind:ref`, following existing
manager-write commands. No flag means the signed-in person's write without
worker attribution. A claim snapshots the binding before lookup and verifies
it again just before sending. Worker direct references use the existing
server-target helper; the server resolves their App and enforces the manager
arm. App mode uses the existing App-target helper. Instruction references
are passed unchanged; refusals never trigger a retry as the person. The CLI
does not manufacture an instruction from a task or chat message.

New reads carry no worker session. Existing worker-get side reads that serve
rendered signature/trailer retain their separate attribution contract.
The marker fields are selected only by the new headerless leave-alone read.

The controls only record state. They launch, stop and relaunch nothing.
`hostDesiredState`, the host-up server adapter and timer are separate slices.

## Evidence

The first commit contains failing command tests only, against fake GraphQL.
Pins cover exact enum/boolean inputs, omission, repeat marker calls, nullable
JSON, worker marker read-back, headerless reads, instruction attribution and
server refusal mapping. Guard mutants are run one at a time and restored.
The final SDL is exported without edits from merged server revision
`79652a984` (server#4242), which includes server#4241 hostSetup.
`make schema-check` verifies the generated client against that exact revision.

## Compatibility review fold

Wolfgang #107896/#107901 chose split documents, not a raised minimum for all
Agent/Worker commands. Lifecycle create/update flags, leave-alone and sweep-mode
require server #4242 (schema skew exits 2). Legacy shared fragments and omitted
flag paths retain their earlier server compatibility. The second red-only
commit pins the legacy documents, old-schema Agent get and read-only marker form.
The final export uses the merged revision above; snapshot changes are generated
from that pin, never hand-edited.


## Final S3 visibility fold
Worker.leaveAlone is nullable: profile-only callers receive null for all four leaveAlone* fields. The isolated read DTO preserves JSON null and renders `unknown (not visible to you)` for the marker, while visible true/false and mutation replies retain boolean values. Existing worker get/list documents remain unchanged. Regression `TestS3WorkerMarkerHidden2067` reproduces the former false rendering before the fix. The final merged SDL preserves this nullable contract. It removes WAITING_ON_CHILDREN: a short-lived worker waiting on child tasks stays up under G7.
