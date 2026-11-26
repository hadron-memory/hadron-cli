# Retiring specs through the retireSpec door (cli#1736)

Design as built. Server contract: hadron-server#3429 (`retireSpec`, provider
hadron-server#4669), cor:spc:010:02, rulings 0212-0214 and 0218-0220.

## Why

`spec rescind` and both `spec supersede` modes retired a spec through separate
reads and writes with client-side rules. None of it was atomic: a rescind and a
supersede could both succeed. The server now does it in one transaction.

## Flow

1. One pre-read (`GetSpecNodeForEdit`) for the node id and revision. It decides
   nothing about whether the spec may be retired.
2. Create mode only: allocate or take `--to`, then `createSpecNode`.
3. One `retireSpec` request. Never resent.
4. Success is rendered from the answer; nothing is read after it. The register
   lookup for the reminder happens before the request.
5. On a conflict or an unusable answer, one `GetSpecRetireState` read of the spec
   (and the successor) describes the current state.

All of this lives in `internal/cmd/spec/retire_door.go`.

## How an answer is classified

| Answer | Outcome | Exit | Re-read |
|---|---|---|---|
| node with the id sent, tagged `superseded`, and for SUPERSEDE an edge to the successor sent | retired | 0 | no |
| validation errors naming only `retireSpec` / `RetireSpecInput` | refused (unsupported) | 2 | no |
| `NODE_WRITE_CONFLICT` | refused | 5 | once |
| `NodeAbstractTooLongError` | refused | 2 | no |
| other coded error at the mutation field or above; HTTP 401/403 | refused | the code's own | no |
| anything else: transport failure, internal fault, error below the field, short success | unknown | 7 (no answer) or 1 | once |

"Coded error at the field or above" is `api.IsRefusedBeforeCommit`. A deeper
path means the server failed while rendering a result it already had, so the
retirement may exist.

## Wording rules

The conflict reading is current state, judged in the door's order: retired, linked
(a hidden target gives the generic sentence), revision moved, successor unusable,
otherwise "the cause isn't visible to this read". Nothing claims what an
unanswered request did. A linked but unretired spec gets "needs manual review; the
CLI makes no automatic repair" and no edit recipe.

## Deliberately kept client checks

These are input checks or normal reference resolution, not retirement rules:
the citation arguments, the spec-ness of the retired node, `--to` being free and
not the old citation, legacy number allocation, `rescind`'s required wording, and
refusing a blank `--reason` / `--summary` before create mode can leave a
successor behind.

## Decisions and limits

- `--authored-with` reaches `retireSpec` for all three verbs when given, and is
  never inferred. The gate's "rescind sends none, no flag" predated the flag
  (cli#2018); Xanthos ruled it superseded (team chat #151150).
- No capability probe: create mode against a server without `retireSpec` creates
  the successor, then reports it with the unsupported refusal.
- `--json`: `unchanged` is gone, `alreadyRescinded` is always false, `wording`
  and `abstract` are null unless the server answered success.
- The schema snapshot already held the door: pinned at hadron-server
  `251162799b` (contains `cc9f2ce39c`); `make schema-check` against
  `27a0206435` shows no drift. No snapshot change.
- No spec citation is proposed: the rule is cor:spc:010:02's.
