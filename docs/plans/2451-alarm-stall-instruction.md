# Alarm acknowledgement and stall-schedule instructions

Implements [cli#2451](https://github.com/hadron-memory/hadron-cli/issues/2451),
a child of #1617, under [CHECKED T1 + security](https://github.com/hadron-memory/hadron-cli/issues/2451#issuecomment-6977266465).
Server minimum ba077810; existing cor:dmo:050:12 and cor:agt:050:24 govern it.
No corpus rule changes.

Separate instructed documents preserve no-flag documents and outputs. Ack's
ordinary authored owner-in-chain path stays unchanged; set's ordinary write
remains the person. Claims parse before requests, after pre-existing usage
checks. Snapshot helpers run before any lookup and recheck before the mutation;
lookups carry neither claim nor session. Instructed ack suppresses ambient
middleware attribution so a failed snapshot cannot borrow a replacement session.
The shared instructedAsPerson fallback remains authoritative. The server checks
App authority and claims, with no plain retry. Only set gains the flag.

Instructed ack captures extensions.warnings under acknowledgeAlarm and adds the
existing warning DTO shape to JSON; text emits warning code/text on stderr.
No-flag JSON stays byte-identical. The first acknowledgement still stands.

Validation uses root command fake transports for both plain/instructed documents,
per-mutation claim/session, zero-call malformed claims, headerless pre-reads,
person fallback and refusal/no-retry, plus warning text/JSON. Named mutants only:
drop each mutation instruction and drop session attachment. Mutants use a private
GOCACHE. Fixtures do not establish live server authorization or activation.
Review seats are distinct cross-family engineers, scoped security every head,
and independent compiled CLI QA; UX waived by gate.
