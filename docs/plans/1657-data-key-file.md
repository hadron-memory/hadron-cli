# memory encrypt / link-user: --data-key-file, literal --data-key deprecated (#1657)

Ruling 0148 (Holger, 2026-11-04): a raw data key in argv is visible in `ps`,
`/proc/<pid>/cmdline` and shell history, so it is deprecated in two phases. This
change is phase one; refusing a literal is a later, separate change that names
its release. Spec: cor:sec:040:04 (tunable list). Nothing is cached (ruling 0129).

## Behavior

For both `memory encrypt` and `memory link-user`:

- `--data-key-file <path>` reads the key from a file; `-` reads stdin. The file
  is checked like `readPassphraseFile` (one shared body, `cmdutil.ReadSecretFile`):
  a file that group or others can access is refused, "... accessible by group or
  others (mode 0640); run chmod 600 on it. Nothing was sent", as is a file that
  is not a regular file or changed while it was opened. Errors name the flag,
  never the path. The file is read, and its mode checked, before any request.
- `--data-key -` is unchanged. For both stdin forms the confirmation is answered
  before stdin is consumed as the key.
- `--data-key-file` is exclusive with `--data-key` and, for `memory encrypt`, with
  `--passphrase-file`; an empty `--data-key-file=` is refused (presence by
  `Changed`, cli#1340). A file with no key is refused.
- A literal `--data-key <value>` still works and prints one stderr line that names
  `--data-key-file`, says it is deprecated and never echoes the key. `-` and the
  file form print nothing. `--json` stdout, exit codes and the request bytes are
  unchanged; the link-user post-check ("a data key was supplied but the server did
  not apply it") is untouched.

## Out of scope

Refusing a literal (its own follow-up); how the key reaches the server (header
versus argument: cli#1650 / hadron-server#1796); any key cache, keychain or
environment form.

## Controls

Red-first `48c9552a`; the implementation is `99ad65e7`. Pins: the file form sends
the same operations, bodies and header as `--data-key -`; 0640/0604/0644/0660/
0666/0601 refused before any request, 0600/0400 accepted; directory, missing,
blank, empty-value and exclusivity refusals; the prompt before stdin; the warning
exactly once without the key in stdout or stderr; help names the file form first.
Mutants (mode check off, presence by value, key echoed, warning on `-`, warning
twice, either exclusion dropped, key untrimmed, link-user warning dropped, stdin
read before the prompt) each fail a named test; dropping the empty-value guard
alone is equivalent (the shared reader refuses an empty path with the same text).
