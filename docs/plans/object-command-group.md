# `hadron object` — object-store command group (#272)

The CLI surface for the **object store** (cor:api:170, server #745; GraphQL #747,
MCP #748) — the legible, collection-oriented CRUD-and-query layer over structured
storage. An object IS a node with an `objectType`, presented as a flat record
`{ id, type, ...fields }`: `id`/`type` are the reserved envelope, the node's typed
`properties` are the top-level fields, and `loc`/`name` are auto-derived and hidden.
It sits on top of the node-flag substrate already shipped (#265/#267, #268/#269);
these commands are the friendly framing.

## Commands

| Command | GraphQL |
| --- | --- |
| `object create -m <mem> --type <t> --fields <json>` `[--key <k>] [--name <n>]` | `createObject` |
| `object get <ref>` | `object(ref:)` |
| `object update <ref> --fields <json> [--reason <r>]` | `updateObject` (atomic shallow **merge**) |
| `object delete <ref> [--hard] --yes` | `deleteObject` (soft default, non-recursive) |
| `object find -m <mem> --type <t> [--match <json>] [--where <json>] [--sort <json>] [--limit N] [--offset N]` (alias `ls`) | `findObjects` → `{ objects, total }` |

## Everything is JSON

The server represents objects and the `fields`/`match`/`where`/`sort` args as the
`JSON` scalar, so the whole surface carries raw JSON in and out (`json.RawMessage`).
That keeps the CLI thin: it validates *well-formedness* client-side (a usage error
before any round-trip) and passes through; the **server owns** schema conformance,
the reserved-`id`/`type`-field rule, the natural-`key` single-segment rule, the
where/sort grammar, and session-on-encrypted — all surfaced verbatim via
`api.MapError`. `object get` maps a `null` result (not an error) to exit 4.

Output: a single object prints verbatim under `--json` (the server's flat record is
the stable contract — there is no gen struct to drift) and pretty-printed otherwise;
`find` uses an explicit `{ objects, total }` DTO with `objects` initialized to `[]`.

## Ref handling

`object get`/`update`/`delete` take an object id **or** a node URN. The server's
object ops forward `ref` to the node surface (`node(ref:)` / `nodeRef`), which
dispatches ID-or-URN (spec 007). A new `cmdutil.CanonicalNodeRef` **canonicalizes**
(adds `hrn:node:` to a bare URN, passes an id through) without a `resolveUrn`
round-trip — unlike `ResolveNodeRef`, which would reject a raw object id as
"not a URN". `memoryRef` on create/find goes through the existing
`CanonicalMemoryRef`.

## Schema / codegen

The object surface is merged on the server's `main` but the local sibling checkout
was on a feature branch, so — as with #265 — the committed snapshot was hand-extended
(from `origin/main`'s SDL) with `ObjectList` + the five ops, rather than a full
`make schema` that would drag in unrelated drift. genqlient rejects a `$type`
variable (`type` is a Go keyword), so the GraphQL **variable** is `$objectType` while
the field stays `type: $objectType`; the wire variable is therefore `objectType`.
Optional args (`key`/`name`/`reason`/`hard`/`match`/`where`/`sort`/`limit`/`offset`)
carry `@genqlient(omitempty)` so an unset value is omitted.

## Tests

`internal/cmd/object_cmd_test.go`: create sends `memoryRef`/`objectType`/`fields`/`key`
(and omits an unset `name`) and prints the flat object; `--fields` required +
mutually exclusive with `--fields-file` + invalid-JSON are usage errors; get
canonicalizes a URN and passes an id through, and a `null` object is exit 4; update
merges + carries `reason`; delete sends `hard` and requires `--yes`; find threads
`match`/`where`/`sort` and returns the `{ objects, total }` envelope; a conformance /
reserved-field `BAD_USER_INPUT` surfaces verbatim as a usage error.
