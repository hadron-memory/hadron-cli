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
  Agent DTOs add `lifecycle`, and `agent get` prints it.
- `team worker leave-alone <worker> --on|--off [--reason text]`: exactly one
  true switch, reason only when marking. The server resolves the worker;
  names carry App scope, direct ids/URNs do not inherit ambient App scope.
  The receipt is `{workerId,leaveAlone,reason,by,at}`; nullable fields stay
  null. Existing worker get/list DTOs add `leaveAlone`, `leaveAloneReason`,
  `leaveAloneBy`, `leaveAloneAt`; text shows them too. Reason/by may be masked.
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
The marker fields themselves are selected by the headerless worker reads.

The controls only record state. They launch, stop and relaunch nothing.
`hostDesiredState`, the host-up server adapter and timer are separate slices.

## Evidence

The first commit contains failing command tests only, against fake GraphQL.
Pins cover exact enum/boolean inputs, omission, repeat marker calls, nullable
JSON, worker marker read-back, headerless reads, instruction attribution and
server refusal mapping. Guard mutants are run one at a time and restored.
The schema must be re-exported from the named merge SHA of server#4242 before
this PR freezes; an export from its review head is provisional build input.
