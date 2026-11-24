# host: the exec launch and the reset-context host handler are gone

Unreleased (cli#3017). The CLI's host side no longer launches agents through
the deleted exec path and no longer carries out `worker.reset-context`: no
host this CLI runs declares the capability, so a registration payload can
never offer it. The `hadron host run` poller, `host sweep`, `host status` and
the installed systemd sweep timer all load the same `host.yaml`, so the load
refusals below surface in each of them, not only `host run`.

| A `host.yaml` that… | Before | Now |
|---|---|---|
| declares the `worker.reset-context` capability | loaded; the host offered the capability | refused at load, naming the removal |
| sets the `reset_context:` block (`orca_bin`) | loaded; the block was read | refused at load, naming the removal |
| sets a worker's `recover:` argv | loaded; the argv was trust-checked on reload | loads; the key is ignored and no longer judged on reload (nothing runs it) |

- **The refusals are deliberate and loud** (cor:agt:070:03): a host that kept
  declaring the capability would invite resets it cannot carry out. Remove the
  capability line and the `reset_context` block from `host.yaml` before
  upgrading.
- **A collected `worker.reset-context` is answered, not timed out:** a server
  that still issues the operation to this host gets `FAILED` with
  `HOST_UNSUPPORTED: this host carries out no worker.reset-context
  (hadron-cli#3017)`. The leading code is load-bearing — the server refuses a
  FAILED reset report whose reason carries no allow-listed code
  (`BAD_CONTEXT_RESET_INPUT`) and re-delivers the operation.
- **Legacy host state is dropped, and converges:** a state file written by an
  older binary may hold in-flight `resets` records; the new binary ignores the
  key and forgets them. The server re-delivers any operation it still holds
  and it ends `FAILED` as above — a dropped record reaches a terminal outcome
  rather than resuming half a reset. No reset could complete under the old
  code (its idle proof never succeeded), so no host holds a completed reset
  this drop could lose.
