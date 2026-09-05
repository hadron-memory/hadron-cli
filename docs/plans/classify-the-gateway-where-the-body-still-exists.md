# Classify a gateway 5xx where the body still exists

Design-as-built for **#544** (filed by @Vera while writing a test for #543).

`exitcode.go` has always said this:

> **Unavailable (#394)** is the one failure the server never refused: the
> request did not reach it, or its answer did not reach us — **a gateway 5xx**,
> a reset connection, a timeout.

A gateway 5xx is the **first example in that list**, and it was the one that did
not work. Every practical gateway 502 exited **1**, with no idempotency caveat,
on the failure class #394 exists to separate.

## Why the guard could not fire

`classifyTransport` asked the right question of the wrong evidence:

```go
if len(httpErr.Response.Errors) > 0 {
    return transportFailure{}, false   // "the API formed an opinion"
}
```

The reasoning is correct — a 5xx carrying real GraphQL errors *is* the API
refusing. The problem is that **`Response.Errors` is never empty**, because
genqlient synthesises it (`graphql/client.go`, v0.8.1):

```go
if err = json.Unmarshal(respBody, &gqlResp); err != nil {
    return &HTTPError{
        Response:   Response{Errors: gqlerror.List{&gqlerror.Error{Message: string(respBody)}}},
        StatusCode: httpResp.StatusCode,
    }
}
```

Any body that is not JSON becomes a one-entry list holding the raw body — and
`<html>502 Bad Gateway</html>` is exactly that. So the gateway branch was
reachable only for a gateway returning **valid JSON with no `errors` array**,
which gateways do not do.

**The evidence the guard needed was destroyed before the guard ran.**

## The fix: ask the body, at the last place it exists

Classification moves into `bearerDoer` — the auth wrapper every request already
goes through, and the last point holding the raw bytes. On a 5xx it reads the
body and applies `hasGraphQLErrorsEnvelope`:

- **envelope** → hand the response back intact; genqlient parses it and the
  existing status/extension mapping applies unchanged;
- **no envelope** → return a typed `*gatewayError`, which `classifyTransport`
  recognises directly.

That also makes this path **agree with `hadron api`'s** (`transportStatus`),
which has classified an HTML 502 correctly all along. **That divergence is what
hid the bug**: the same question was answered in two places, and only the answer
nobody tested was wrong.

Two deliberate choices inside it:

- **The body is read in full, not to a cap.** genqlient's own error path does
  `io.ReadAll` with no limit, so this adds no exposure — whereas a cap adds a
  failure mode, truncating a large but legitimate `errors[]` into invalid JSON
  and reclassifying the API's opinion as a lost request.
- **A read that fails partway is a lost answer**, not a refusal: headers
  arrived, so the request reached the server and a mutation may have committed.
  Same reasoning as PR #415's `io.ErrUnexpectedEOF` case.

`classifyTransport` keeps its old `5xx + no errors` branch as a **fallback**,
labelled as one. Through this CLI's client it is now unreachable — the doer has
already removed the gateway cases — but the function is package-level and the
rule it encodes is still true. What it can no longer pretend to be is the *real*
classification: the body it would need is gone by then.

## The tests were the actual defect

The suite had cases for this branch, and they passed, and they were meaningless:

```go
{"gateway 502 with a non-JSON body is transport", &graphql.HTTPError{StatusCode: 502}, exitcode.Unavailable},
```

**A hand-built `HTTPError` with no `Errors` is a shape genqlient never
produces.** The fixture had stopped describing the server and started describing
the test — the same trap #552's plan doc names about `irisWorkerJSON`. So the
branch was covered, green, and unreachable in production simultaneously.

The new tests drive a **real `httptest` server** through `NewClient`, so the
error is whatever the client actually builds. Six body shapes, including two
positive controls (a 500 with a well-formed envelope, and one carrying only
`message`) that a fix making every 5xx transport would fail.

## Five mutations, and two of them were about my own probes

| mutation | reds |
| --- | --- |
| doer stops classifying | the four gateway cases |
| envelope check removed (every 5xx a gateway) | both positive controls |
| body not restored after the peek | the body-survival control |
| `classifyTransport` forgets `gatewayError` | the four gateway cases |
| `readErr` ignored | the cut-short case |

**Mutation 3 was green twice, for two different reasons, and neither was "the
guard is fine".**

First it was green because *the mutation did not compile* — deleting the restore
left `bytes` unused, my harness ran `go build … && go test …`, and the `&&`
short-circuited so **no test ran at all** and the grep found no failures. A
textbook `review:a-mutation-check-can-itself-be-a-no-op`: the probe misfired and
looked exactly like success. Redone as an edit that compiles (restore an *empty*
body).

Then it was green for the *second* reason that node names: the mutation landed
and **no fixture could see it.** Destroying the body also produces exit 1, and
every envelope case asserted only the exit code — so "the API refused" and "we
broke the body on the way through" were indistinguishable. Fixed by asserting
the server's own message survives, which can only be true if the bytes were
handed back.

**Mutation 5 was green the first time too, and that one indicted the fixture.**
A truncated body that is merely *invalid JSON* fails the envelope check anyway,
so it cannot tell "we honoured the read error" from "we classified the bytes we
happened to get". The fixture now sends a **complete, valid envelope** short of
its promised `Content-Length`: ignoring the read error scores it a refusal (1),
honouring it yields 7. Only then does the branch have a test.

## Propagation

- **No doc change, and that is the point.** `agentic-usage.md` already
  documents *"7 — the request never got an answer (gateway 5xx, reset,
  timeout)"*. The docs were right; the code was wrong. The fix makes the
  documented contract true rather than changing it.
- **No spec.** Client-side error classification against an exit-code contract
  that already exists in `internal/exitcode`. Nothing crosses a repo boundary.
- **One observation, noted not fixed:** `MapError`'s `HTTPError` branch keys on
  the HTTP **status** and returns before `codeForExtension`, so a 5xx carrying a
  typed extension code still exits 1. Out of scope here, and a different
  decision with its own blast radius.
