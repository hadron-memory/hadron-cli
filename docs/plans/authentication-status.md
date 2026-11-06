# Authoritative authentication status (#1726)

Built contract: cor:aut:010:05 rev 3, Holger ruling 0224; gate #1726's
CHECKED build slice. Server provider: #3537 (32c87e54, for #3532).
This is a source compatibility requirement, not a deployed or released floor.

`auth status` makes one public `CheckAuthenticationStatus` GraphQL query,
selecting only `authenticationStatus`. The ordinary credential resolution
chain remains HADRON_TOKEN, live impersonation, stored login. No credential
also sends one query, without Authorization. No retry or authContext fallback.
The response is constant-sized regardless of tenant data (roughly 50 bytes for
an enum-only successful GraphQL body; error bodies vary).

| Server answer | Text | Exit | JSON |
|---|---|---|---|
| AUTHENTICATED | credential authenticated | 0 | {"status":"AUTHENTICATED"} |
| REFUSED | a credential was presented and the server did not accept it | 3 | {"status":"REFUSED"} |
| ANONYMOUS | no credential was presented | 3 | {"status":"ANONYMOUS"} |

No reason, cause, credential, identity, source or key metadata is added to an
answer. No-answer cases return errors, never a synthesized REFUSED. Missing,
null and unknown status values exit 7. Ordinary mapped errors retain their
exits, except an error that maps to 3 becomes a generic check-failed error
(exit 1), without its alleged authentication cause. This includes OAuth
scope failures before the resolver runs; they are not an enum answer.
An old server's unknown-field error remains explicit (exit 2). A server
internal error follows the shared code/requestId concealment policy.

## Deliberate output change

The ruled answer-only JSON replaces the former `server`, `authenticated`,
`tokenSource`, `tokenStorage`, `user`, `principalType`, `key`, `impersonating`
and `rejectedReason` report. Scripts must read `status` instead of
`authenticated` and distinguish check errors from status results. This is
intentional under ruling 0224, not an additive JSON change. `auth whoami`
keeps its operation, DTO, errors and exits; only its stale help pointer to
status's removed detail report is corrected. Token validation is unchanged.

## Acceptance and evidence

- Three answers in text and JSON, one request, raw status and exact keyset.
  A locally supplied credential answered ANONYMOUS is not inferred REFUSED.
- Signed-out request has no Authorization, gets ANONYMOUS and exits 3.
- Missing/null/unknown answer cannot become a credential verdict.
- Validation, internal, UNAUTHENTICATED, HTTP 401, gateway and OAuth scope
  faults exercise no-answer behavior; renderer exits and stderr are checked.
- Older detailed-status tests are replaced by the ruled contract. Other
  command scope tests remain, as do whoami's tests and unchanged code path.
- Coverage matrix: `authenticationStatus  command auth status`.

Red baseline a977dd2d compiles and fails the new answers/no-answer tests for
the intended behavior gaps. Earlier test-only 34bf08e8 had a Builder.Bytes
compile error, corrected before any production implementation; that failed
compile is not acceptance evidence. Initial generated operation name collided
with the schema enum; renamed CheckAuthenticationStatus before validation.

Author tests and compiled fake QA establish the client contract, not live
credential revocation, cross-tenant behavior, the equality of real refusal
causes, provider deployment or token timing. The server retains those duties.
Gate T2: distinct cross-author-family LOCAL/FULL BLIND, scoped security every
head, QA through CLI; no release or deployment action.
