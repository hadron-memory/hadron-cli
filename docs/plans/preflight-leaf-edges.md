# Preflight: leaf-owned route edges (cli#1025)

Design as built. Holger's ruling on hadron-server#1797: an edge points from the
more specific node to the more general one, so a preflight leaf owns the edge to
the router (or to an area parent), labelled `to <action>`.

## What shipped

- `--route-model leaf|mirrored` on `coding preflight create|route|list|lint`,
  resolved flag > `preflightRouteModel` in `.hadron/config.json` > `mirrored`.
  `mirrored` stays the default and its output is byte-identical, so memories on
  the mirrored-pair model (mm-app) are untouched. Flipping the default and
  migrating mm-app is a separate decision (Holger, the mm owners), not made here.
- `create`/`route` under `leaf` write ONE edge, node -> attach target
  (`--attach-to <loc>`, default `--root`). Nothing on the router's outgoing set,
  no back-edge, body line off unless `--section` (it splices into the attach
  target). `create` embeds the edge in `createNode` and confirms it landed.
- `list` unions incoming and outgoing edges, one row per node with a `model`
  column (`leaf`, or `legacy` for a router-only route); `--json` carries `model`
  under `leaf` only. `lint` carries label / retired / moved-memory to incoming
  edges, drops `route-target-resolves` for them, and adds `legacy-outgoing-route`
  and `leaf-missing-edge`.

## Decisions and limits

- `leaf-missing-edge` scans the router's own body only (no area-node fetch);
  area bodies are a follow-up.
- Incoming edges whose source the server redacts are not counted; lint prints
  `N incoming edges visible to this caller` and never claims completeness.
- Every incoming edge is a route. On hadronmemory.com:dev that is 129 edges, 40
  of them not `to <action>` (`routed-from`, `Applies when`, ...), which lint
  reports as `route-label-phrasing`. Filtering by label would hide exactly the
  defect that rule exists for; whether those edges belong on the router at all
  is a question for the memory's owners.
- Scale: the incoming read is one `GetNode`, unpaged; the dev router's 129
  incoming edges all came back (checked against the real server).
- A leaf in another memory owns an edge written in that memory; the server
  decides whether the caller may.
- hadron-server's own `preflightRouteModel: leaf` setting is a change in that
  repo, filed separately.
