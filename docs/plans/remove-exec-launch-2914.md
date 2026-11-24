# Remove the transitional exec launch path (cli#2914), as built

RULED by Holger (2026-11-22, via the specs engineer): the exec launch path is removed. The gate decided what
an empty `launch_via` means; the scope split (reset-context and the exec agent's machinery go in a follow-up,
https://github.com/hadron-memory/hadron-cli/issues/3017) was decided by the coordinator.

## What changed
- **Default.** An empty `launch_via` means the supervised (Orca) launch; `launchesViaOrca()` is true for empty
  and `orca`. An explicit `launch_via: exec` is refused at load: `launch_via: exec was removed ... use
  launch_via: orca (or leave it empty) with a launcher_profile and an absolute disk_tmp`. Any other value is
  refused as before.
- **Prerequisites.** Every worker now needs a `launcher_profile`, an absolute `disk_tmp`, an
  `account_profiles` entry and `orca_launch.orca_bin` (the existing messages). An old exec-style worker
  (a `launch` argv, no profile) is refused with: `launch (an argv) belonged to the exec launch path, which was
  removed; set a launcher_profile (and an absolute disk_tmp) instead`.
- **worker.start** has one path. The exec branch, its same-family guard (the supervised path's `ResolveRun`
  decides the family) and its "settings not applied" statement are gone.
- **Reload.** The "this poller did not wire Orca at start" refusal is gone (every poller wires it, so it could
  not fire); the launch argv is no longer judged as a program (the account bin is).
- **host import** reads a legacy exec-style config through `LoadConfigForImport`, which validates everything
  except the per-worker launch-path rules.
- **Docs.** `host run --help`, the quickstart, the sample YAML, `host sweep` help and agentic-usage.

## What an operator sees
An old host.yaml (`launch: [...]`, no `launch_via`) no longer loads: `host run` exits 2 before any server
call naming the worker and what to set. `launch_via: exec` the same. A worker whose family has no launch
template (anything but claude, glm, kimi and codex) can no longer start at all: its fixed argv was the only
way to run one. A non-Linux host can no longer launch workers (Orca needs Linux, pidfd). Every host now wires
the relaunch owner (the periodic recovery pass) and reads `HostRelaunchSnapshot` each run (plus, since #2816,
the targeted `HostRelaunchWorker` read whenever a freshness check is about one worker).

## Tests
- The hostpoller fixtures: `testPoller`'s worker is supervised and its fake host records the supervised launch
  as a launch (`fakeHost.orcaLaunch`), so launch assertions keep their shape (argv is now the profile's, the
  environment the explicit list). YAML fixtures keep the old `launch: [program]` line as shorthand that
  `hostyaml.Supervised` (test support, imported only by tests) renders into a profile, `disk_tmp`, account and
  `orca_launch`; text without the shorthand is unchanged.
- Deleted because they pinned the removed behaviour: the exec-default pin, the exec "settings not applied"
  statement, the exec-host sweep outage exit.
- Changed in meaning (each says why in a comment): the family tests (the supervised refusal text; families
  without a template are refused), the relative `launch` program (an account bin refused at load), a relative
  `disk_tmp` (refused by the absolute `disk_tmp` rule), the Orca-wiring reload test (a worker a reload adds
  now uses the wiring made at start).
- New: the supervised default in the worker's existing worktree (cor:agt:070:08), the refusals, the
  prerequisites, the legacy import loader, and `host run` refusing before any call.
