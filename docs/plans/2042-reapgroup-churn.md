# Process-group scans on a churning host (cli#2042)

The Linux drain scanner must prove a group empty before reaping its held
leader. A listed PID that disappears before stat may have forked a member;
therefore it invalidates the snapshot even when the disappearing PID was
probably unrelated. Eight invalid passes remain an unproven drain.

The scan now waits 2 ms between invalid passes, at most seven waits per
call. This gives a short burst time to settle without spinning through the
whole retry allowance. Decisive passes return immediately. No scan result,
PID ownership rule, stop timeout or watcher interval changes.

This does **not** guarantee eventual drain under continuous churn. The
watcher still retains the zombie and its allocated group number until a
complete snapshot proves drain. A local experiment adding paced retries
and a 10-second eventual-drain deadline still failed 3/3 on the shared host.
Reaping on elapsed time would sacrifice the existing group-number reuse
safety; the gate explicitly assigns that policy decision to the coordinator.
Ada chose the unbounded conservative hold in team chat #106929 and approved
the owned-process zombie fixture in #106932. Liveness under sustained churn
is not guaranteed. No reap-on-timeout behavior is implemented here.

## Controls

- The red commit adds a retry-wait seam without invoking it. A deterministic
  burst fixture stays invalid until the second wait; the original loop
  exhausts eight listings with zero waits and fails both retry controls.
- A sustained-churn fixture asserts eight listings, seven waits, and a
  conservative answer, then verifies recovery on a later clean call.
- The real watcher holds an actual unreaped child across exhausted scans,
  then reaps on the next clean tick. No group-number signals are used.
- Empty, unreadable and live-member decisive passes return without waiting.
- The zombie-state test reads real stat files for all three processes it
  owns, while its directory listing is restricted to that complete fixture.
  It no longer asks unrelated host activity to stop for the zombie-state
  assertion. Churn safety is exercised independently by the controls above.

The mocked wait advances a logical burst clock rather than sleeping; the
production wait uses time.Sleep. It does not claim a real busy host supplies
a clean snapshot within any fixed duration. This internal Linux host
implementation has no cross-repository contract or new corpus surface,
consistent with Victor's FIT on the issue.
