# Node-only canonical open (#1015, Refs #29)

Gate6357562329, explicit Node-only selection Xavier32018. Memory/Agent/App
have no portalUrl in the inspected merged provider812b4fac; provider#2309 owns
that prerequisite. No guessed routes or unavailable-kind command stubs.

`node open <ref> [-m <memory>] [--print]` normalizes refs locally, preserving
qualified refs even alongside memory. One NodePortal query selects id,urn,
portalUrl only. Missing/unreadable is one exit4; provider failures pass through.
URL null/invalid is explicit unavailable7. Validation parses http(s), requires
host, rejects userinfo/opaque paths. Output copies the ORIGINAL returned URL,
not a reconstructed URL, so escaping/custom origins/path remain provider-owned.

A non-terminal stdout, JSON or --print never calls the opener. Interactive
text requests auth.OpenInBrowser (platform argv, never shell), then prints URL.
We do not say the page loaded: the existing helper starts a launcher process,
not a browser-page health check. An error from the helper returns failure with
--print guidance, no success receipt. Private URL credentials are rejected.
The navigation boundary is injected for tests; no real browser/external visit.

Stable JSON {id,urn,portalUrl}; no mutation, content/decryption or lookups beyond
the one authorized query. Red-first4ef87c1 command tests; fake opener controls
separately verify all print/navigation paths. No provider policy/spec change.
