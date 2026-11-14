# Typed spec tooling

Part 1 of cli#2018; CHECKED 6874714119, FIT 6874667050, AT0.7312.
Part 2 is Vera's AT0.7310 under server#1975. This change does not edit the
corpus, activate grants, add revoke, or retire PrincipalGrant. Part 3's
person-list sub-issue is cli#2021, awaiting its own T2 gate/dispatch; existing
worker/manager commands retain #1832/#1846 ownership. The proposed --fix is
not implemented: this slice reads and reports findings, never auto-rewrites specs.

## Frame source and behavior

For `hadronmemory.com:specs`, `spec lint` also checks the platform frame.
`--frame` opts another explicit corpus into that same authoring framework
(and can be used when addressing the platform memory by id). Each needed
`spec.<type>` reads its raw `authoring:<type>:template` node from
`hrn:mem:hadronmemory.com:specs-engineer-system` once per invocation. Section
order, required/optional sections, Built-line presence, defining caps and
below-line sections come from that template, not per-type lists in Go.
This is a live read dependency: unavailable, denied or unrecognized template
content fails the command rather than returning a clean partial report.
There is no persistent template cache or new API operation.

The lint reports framed names without the typed name/citation suffix, tags,
H1/status/Built errors, missing/extra/reordered sections, caps/divider errors,
missing/over-1,600-character abstracts, host-specific portal links, unresolved
corpus links and labels differing from the target's current name without its
citation suffix. Link checks read raw nodes and cache targets within the call.
The legacy lint findings remain. Frame findings are errors (exit 5), even
without --strict; generic abstract-length remains advisory on its own.
Other corpora keep their existing checks unless --frame is requested.
The historical exemption covers structure only. Link rules remain corpus-wide,
including retired and pre-frame specs, matching speccheck.py links(). A clean
mechanical check is not factual verification or approval.

The committed Rule template is a test fixture copied from the authoring node
(version 1.2.0); production always reads Hadron. Tests change its sections to
show that changing the input template changes the lint result. They also
assert raw query/ref selection, target caching, and refusal on denied reads.
An author-only audit parsed all ten templates downloaded from Hadron.

## Names and authoring

A typed spec edit keeps the explicit --title literal, as spec new --role does.
Legacy names retain `<citation> — <title>`. The stored node id/revision guard,
proposal hash and approval invalidation are unchanged.

Rescind accepts the existing explicit --authored-with/--authored-revision
pair and carries the claim in its one guarded updateSpecNode input. No task
is inferred. Pair validation occurs before reads, and write-time role rules
still decide whether the claim is accepted.

The committed server API exposes MemoryConfig rules only to managers, not
a general authoring validation read for every writer. server#4168 tracks
that missing surface. Per the gate's explicit fallback, edit dry runs state
that the authoring check runs only at write time; JSON adds authoringCheck
only to a dry run. Neither a missing nor a named claim is called validated.
There is no speculative write/probe or hard-coded role-to-task map. Memory
write-access checks still run where supported. Passing a preview does not
promise the write will pass its authoring checks.

## Evidence and limits

Red-first commit 19cf0706 fails on main 4ffaabeb for all four gap categories:
missing frame name/tags findings, legacy prefix on a typed title, unknown
rescind authoring flag, and an undisclosed dry-run authoring limitation.
The latter is the gate-approved honest-output alternative, not server-side
validation implemented by this CLI.

A read-only live check of cor:acl:140 reported the same 20 frame and 29 link
findings as the canonical speccheck.py 1.2.0 run against a captured corpus
snapshot, alongside the existing lint findings. No production spec was
written, retired or repaired. Corpus revisions can change after this read;
there is no cross-request snapshot guarantee and no claim Part 2 is clean.

Focused command/spec tests run with synthetic HTTP; no live governed write
was attempted. Cross-family LOCAL, BLIND and compiled-CLI QA are required by
this issue's T1 stamp before readiness. UX waived and security n/a by stamp.

Review correction: malformed type names are per-node frame-role errors; the
remaining corpus is still checked. Unavailable template evidence remains a
command-level failure. The help inventory indexes command pages, not flags or
Long prose: no new page is added by --frame, and help-inventory-check verifies
the unchanged row set as well as the version pin.
