# C5 host-local relaunch (#2060), work in progress

This branch contains the guarded relaunch core and Linux journal. It does
not yet wire a production relaunch adapter or the host-up config flag.
The command-level dry-run test is intentionally red until that integration.
No activation or successful production relaunch is claimed.

The pure `internal/hostrelaunch` Runner consumes an exact App, host, worker,
open session, server and absolute worktree identity. It rechecks desired
RUNNING/APPLY state, binding identity and process evidence before and after
acquiring the worktree launch lock. Stops, unknown reasons and unproven
observations refuse. A typed profile snapshot supplies the launch callback;
its preparation must not write or launch anything. Dry runs acquire no locks,
write no journal, and emit no notice or session log.

An applying sweep claims a persisted generation. Two adjacent generations
must observe absence under the same identity and profile. An intervening
sweep without that observation breaks continuity. Attempts are per worker,
across sessions and profiles, persisted before launch and limited to three
per rolling hour. Future timestamps remain counted when the clock moves
backward. After launch the adapter must identify exactly the returned pinned
agent and recheck desired state and binding. Failure rolls back only that
returned agent, never another terminal selected by family or worktree.

The Linux journal holds a nonblocking kernel run lock, uses private owned
directories and regular files, refuses links/FIFOs/corrupt or oversized JSON,
and writes through an fd-anchored temporary file, fsync and atomic rename.
Every applying open persists its generation, including empty sweeps. Read-only
opens never create directories, lock files or state. Restart tests pin the
attempt cap and absence-continuity interruption; race tests and package lint
pass. Other OS storage adapters are not provided on this branch.

The separate strict census helper requires an explicit observer-authority
callback before and after its scan. It includes the caller and its ancestors,
refuses partial live entries and PID identity changes, and retains no argv
in its receipt. Raw multiple matches conservatively refuse, including ambiguous
shim chains. Empty live command lines are unproven, not assumed harmless.
The existing observational duplicate-cleanup scan is unchanged.

## Integration dependencies

Jill confirmed in team chat #108530 that C3 supplies typed launch profiles,
`orcaLaunchSpec`, host-side pidfd-pinned OrcaAgent identity, Done and Terminal.
Only the launched agent is placed in the private PID namespace. The poller
normally remains outside it. C3 serializes its own in-memory agents under
p.mu, but supplies neither a host-global census witness nor a cross-process
start lock. No tracked agent after a poller restart is not absence proof.
A host-up process invoked inside an agent namespace cannot infer host-global
absence from its local proc view. Wolfgang's ruling (#108541) selects fail-closed operation without a new
Orca census or global lock: relaunch only inside the host-namespace poller,
sharing p.agents/p.mu; tracked absence requires pinned Done, and untracked
absence requires two sweeps plus a strict zero-agent host proc census.
Unreadable proc, ambiguous cwd, scan errors or unknown visibility report
RELAUNCH_UNPROVEN and never launch. No launch adapter is enabled yet.

Wolfgang confirmed the source in #108559: the host poller captures its own
/proc/self/ns/pid identity at startup, under the host launch contract that
it is never launched under unshare. The constructor now captures the nsfs
device/inode pair once; failed capture remains unknown. HostPIDNamespace
exposes that immutable snapshot and VerifyCallerPIDNamespace checks the
caller's namespace against it. The host-up adapter must read the witness
through that existing poller, never create a new caller-local Poller as proof.
A missing or mismatched witness refuses RELAUNCH_UNPROVEN. No proc/1 read is
used. Namespace mismatch/error tests and the live startup-capture test pass.
Transport through the running poller and the relaunch adapter remain unwired.

The intended adapter must compose C3's typed launch with an empty grant and
its fresh recovery prompt: preserve the open session, run session recover,
never bind, force, end or resume. It must share C3 tracking/serialization,
keep typed preparation free of disk_tmp writes during dry run, preserve crash
work, and verify exactly one process after launch. The optional host-up
config source is transitional pending S1b, approved in #108275.

Server #4247 supplies honest refused/failed repair outcomes. Until its merge,
those outcomes must remain an injectable seam and appear as closed JSON/log
reason codes, never an existing false CAP_REACHED notice (#108275). The
session logging principal and final notice adapter remain integration work.

Review schedule: T2 plus security. Named seats at freeze: Jerry BLIND,
Jill LOCAL, Rodolfo security and Colin QA. No seats are requested while this
branch remains incomplete. Existing platform contracts are consumed here;
this branch does not mint or change a platform spec citation.
