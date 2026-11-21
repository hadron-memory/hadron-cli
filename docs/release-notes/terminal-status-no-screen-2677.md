# `orca terminal status`: `no_screen` and `multiple_terminals` reasons

Unreleased (cli#2677, slice b of cli#2675; the server enum gained both values in
hadron-server#4558). The contract stays `terminal-status@1`: two values join the
closed `reason` list, no key is added or renamed, and `unrecognized` keeps its
value and meaning for a real screen no rule matches.

| Situation | Before | Now |
|---|---|---|
| Orca gives no rendered screen (a read whose source is `screen-unavailable`) | `locked` / `unrecognized` | `locked` / `no_screen` |
| Two agent terminals bound to one worker | `locked` / `unrecognized` | `locked` / `multiple_terminals` (`terminal` stays `null`) |
| A real screen that matches no rule | `locked` / `unrecognized` | unchanged |

- **Status is unchanged:** both new cases stay `locked`, so a consumer that fails
  closed on `locked` is safe. A consumer that keyed on the literal `unrecognized`
  for these two cases now sees the new values.
- **Rollout order:** the server must accept both values before a host emits them
  (the team health report writes the reason to the server).
