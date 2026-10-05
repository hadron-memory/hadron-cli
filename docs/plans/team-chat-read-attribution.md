# Bound chat-read attribution (#817)

Contract settled with Andi at team #6643 before implementation; server counterpart
is #1628 / PR #1633. Schema exported from server revision
`e852b0c2a3c1bc337a9950b8f8862156e25e9e9a` through an explicitly selected
HADRON_SERVER_DIR. Only `advanceReadState: false` disables automatic cursor
writes; omitted, null and true preserve server defaults. Ownership/eligibility,
heartbeat and usage attribution remain. MCP is unchanged. Ada owns the proposed
post-merge `cor:agt:020:04` propagation (#6641); no spec minted here.

The CLI attributes primary pages and the independent chat-head probe only when
its session binding matches the deployment and canonical App and still names
the same session. It rechecks the local binding before every attributed request.
The check re-reads session ID, canonical App ID and a nonempty deployment on
each attributed call; absent deployment provenance stays headerless. The
server remains responsible for session ownership and current eligibility.
Unknown App identity degrades to a readable headerless request, without claiming
attribution. Metadata and cursor diagnostics remain headerless.

Separate generated attributed queries contain literal `advanceReadState: false`.
Legacy queries contain no new argument and no session header. A parsed schema
refusal for exactly the unknown `advanceReadState` argument on
`Query.teamChatMessages` disables attribution for this command and retries the
same read headerless, with an explicit note. Every error in the envelope must
match; business refusals, unrelated validation, raw strings and transport errors
are not compatibility retries. No unsafe session-attributed legacy query exists.

Sending headers only on apparently eligible forward reads was rejected: the
server could already advance before the CLI detects a stale page, fails to
render, or observes a rebind. The existing successful-delivery, scope,
contiguity, duplicate-edge and discrepancy guards still control the explicit
acknowledgement; no acknowledgement logic changed.

Paths checked: head and page success; missing pages; tail, backward, mentions
and windowed reads; stale head/page disagreement; render failure; multi-page
walk; missing session/binding, other App/deployment and rebind mid-command;
unsupported argument at head or page; unrelated/mixed/business failures.
In-flight rebind cannot withdraw a header already sent, but suppression prevents
that read from advancing and subsequent requests stop using the old session.

Author validation: full Go suite; internal/cmd and internal/api race runs; build;
lint; generated-client and unbound-operation freshness. Three mutants killed by
specific FAIL assertions: suppression removed, compatibility fallback broadened,
and binding recheck removed. Equivalent predicate rewrite survives. HTTP fixture
queries and headers are observed directly; this is not production usage-row QA.
Independent server/CLI end-to-end attribution and read-state evidence remain
review/QA gates. On older production servers the expected behavior is headerless
fallback, not an attribution claim.

## Step-5 review cycle

Four local P3s addressed: per-request binding checks now cover App/deployment
as well as session identity; fallback latch and diagnostic are pinned across a
multi-page walk; out-of-scope tests assert legacy operation names as well as
empty headers; refusal wording is explicitly pinned to #1633 and Diego's
real-production validation observation. Missing deployment provenance is
covered too. No persistence of capability state across commands: one refused
probe on old servers is an accepted compatibility cost, not a production
capability cache with stale-lifetime ambiguity.
