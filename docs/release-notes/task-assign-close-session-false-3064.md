# `task assign` / `task reassign`: `--close-session=false` is refused

Unreleased (cli#3064). `hadron team task assign` and `reassign` used to accept
`--close-session=false` and `--no-close-session=false` and silently keep the
task's stored choice, because a plain flag cannot tell `=false` from an omitted
flag. They now refuse it the way `task update` already does.

| Command | Before | Now |
|---|---|---|
| `task assign\|reassign … --close-session=false` | exit 0, stored choice kept | exit 2 `--close-session=false changes nothing; to say the opposite pass --no-close-session`, nothing sent |
| `task assign\|reassign … --no-close-session=false` | exit 0, stored choice kept | exit 2, names `--close-session` |
| `--close-session`, `--no-close-session`, `=true` forms, or neither flag | unchanged | unchanged |

To turn the choice off, pass `--no-close-session`. `task create`, `task clone`
and the `task update` refusal are unchanged.
