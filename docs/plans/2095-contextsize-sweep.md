# Context measurement: one descriptor and one discovery pass

Issue #2095 fixes two reporting-only problems in C8. Neither the server fields
nor their both-or-neither serialization changes.

## Reading a rollout

The production Codex path opens a regular file once. It reads `session_meta.cwd`
and the bounded usage tail through that same descriptor and captured size, then
closes it. Replacing the pathname after the open cannot substitute a different
worktree's token count. This does not protect against a same-uid actor rewriting
the contents of the already-open file in place.

A matching newest rollout with no usable reading still yields no measurement;
an older rollout is not substituted. Standalone `CodexFromFile` remains a usage
reader and shares the descriptor-based tail parser with production measurement.

## Discovery lifetime

Each health-sweep invocation owns one `contextsize.Measurer`, used sequentially
by all of its worker rows. It discovers and sorts a Codex sessions archive once
per cleaned sessions-root path, retaining at most the existing 300 newest
candidate paths. Different account roots have separate candidate lists. Empty
or unreadable roots are also remembered for that invocation.

Only discovery is cached. Each measurement reopens and attributes its candidate
before reading usage. A rollout created after discovery is eligible on the next
sweep; no cache survives the current command invocation. Each root still needs
one archive traversal and metadata sort. Subsequent workers examine at most 300
candidates each. Claude discovery and default/explicit root selection are unchanged.

## Verification

The red-first commit reproduces both failures: a pathname replacement returns
999 foreign tokens instead of the original 42, and three workers traverse a
1,200-file archive three times. The fixed tests require the original descriptor,
one traversal per root per sweep, the candidate-open bound, separate roots,
fresh discovery on the next sweep, fresh usage on repeated reads, and no older
fallback from an unusable newest rollout. A command-level test pins sharing
within a sweep and discarding the instance before the next invocation.

Filesystem seams used by these tests default to the real filesystem. They are
not user-facing flags, and no transcript content is logged or sent.
