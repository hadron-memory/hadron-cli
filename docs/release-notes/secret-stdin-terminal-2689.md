Breaking for interactive use: `memory encrypt` and `memory link-user` no longer
read a key or passphrase typed or pasted at a terminal (`--data-key -`,
`--data-key-file -`, `--passphrase-file -`), and `--unlock-passphrase-stdin` is
refused on a terminal too (exit 2): a pasted secret echoed to the screen.

Pipe the secret (`--yes` is then required on `encrypt` and `link-user`) or use a
file only you can read (chmod 600). `--unlock` without the stdin flag still
prompts with no echo. On Windows, pipe it.
