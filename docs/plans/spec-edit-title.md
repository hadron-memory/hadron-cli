# `spec edit --title` (cli#810)

`spec edit` could change a spec's body, abstract and description but not its title
(`Node.name`), so a title that no longer matched its rule could not be corrected
through the only authoring surface (`hadron spec`).

## As built

- `--title <text>` sets `Node.name` to `<stored citation> — <text>`. The citation
  is the node's own `loc`, never typed or parsed from the argument. A blank or
  multi-line value, or one already starting with `<citation> — `, is refused with
  Usage (exit 2) before any request.
- It rides the **same guarded write** as the other fields: the proposal gains
  `curName`/`newName`, `input()` sets `UpdateNodeInput.name` only when it changed,
  and the immutable node id, expected revision and `proposalHash` (a hash of that
  input) therefore cover it. There is no separate name mutation.
- A title-only edit counts as non-interactive: no editor opens. Omitted `--title`
  preserves the name; a title equal to the stored name is a no-op (`changed:
  false`, nothing written).
- `--dry-run` reports a `name` entry in `changes[]` (stored vs proposed, with a
  diff), makes no write and no status read.
- After a title write the server's returned name must equal the requested one,
  else exit 1 ("can't be confirmed"): a response that disagrees is not a write the
  command can vouch for.
- The title is an approval-hash field (`cor:dmo:120:01`), so a retitle can
  supersede an approval. The command does not compute that: after a successful
  write it reads the approval status back with the existing `specStatuses` batch
  read and reports it (`approvalStatus` in `--json`, an `Approval:` line in text).
  A failed or unsupported read leaves it omitted with a stderr warning or a note;
  it never fails a write that already landed.

## Not done

No server change (the shared update path already takes `name`), no corpus
retitle or backfill, no citation mint.
