# Memory encryption secrets travel in request headers (cli#1650)

`memory encrypt` keeps its input flags and result DTO. Its GraphQL document now
names only `memoryRef`: neither the key nor passphrase is an argument or variable.
This implements cor:sec:040:04 and server#1796 step 1; it changes no server rule.

## Requests and principal

The command removes inherited memory-key and operation-key contexts before its
lookups. A qualified memory reference uses the existing targeted GetMemory read;
a raw id needs no resolve request. After confirmation and secret input, the slim
MemoryEncryptionPrincipal query reads only authContext.principalType. A USER uses
one `{memory: <resolved id>, dataKey}` or `{memory, passphrase}` entry in
X-Hadron-Memory-Keys. An APP completion uses X-Hadron-Operation-Key with a validated
canonical base64 data key. An App passphrase, missing principal or unknown
principal refuses before the mutation. `--app` is context, not the principal.

Exactly one EncryptMemory mutation is sent, with a child context carrying only
that target's secret. Ambient --unlock entries are not merged. Neither lookup
carries either key header. No key is cached or persisted. Typical request count:
2 GraphQL requests for a raw id (principal + mutation), 3 for a qualified ref
(targeted memory lookup + principal + mutation), no corpus-sized reads. Principal
payload is one enum; the resolve is the existing memory projection. No new list.

## Input and failure behavior

Both file forms use cmdutil.ReadSecretFile: regular file, mode 600 or stricter,
identity rechecked on the opened descriptor, no FIFO blocking and no secret path
in diagnostics. Passphrase files are checked before any network request; stdin
still waits until confirmation and drops trailing CR/LF only. Data-key trimming,
literal deprecation warning and output fields are unchanged. --unlock remains
passphrase-only. Windows cannot check these file permissions and refuses file
sources; stdin remains supported through the existing helper's contract.

An older server's NOT_ENCRYPTED header refusal names the server upgrade, not
--unlock. The exactly-one-secret refusal says the key header did not reach the
server. There is no argument fallback or automatic retry on any error. A null
answer or isEncrypted:false is not a success: inspect the memory before retrying.
The existing transport still refuses unsafe redirects/schemes and scrubs header
values from errors at every HTTP status. The dataKey form joins those secrets.
Post-submission command diagnostics use the same spellings (including escaped
JSON header/passphrase echoes), detached from the raw error chain. An answer
repeating a secret is withheld even when isEncrypted is true; metadata never
becomes an echo channel. Bounds are finite, matching existing transport redaction,
not a claim to recognize every conceivable encoding.

## Verification and boundaries

Red-first c9f6f78e adds fake-HTTP pins for documents/variables, target-only per-call
headers, App header/refusal, permission checks, no fallback, old/stripped headers,
false/null success, cross-origin redirects and both secret forms in echoed errors.
Additional pins cover rendered text/JSON header echoes and escaped success data.
`--verbose` and `--debug` are not supported by this command; separate pins show
those parse refusals send no request. Echo tests require a mutation actually ran,
so that parse refusal cannot substitute for transport redaction evidence.
Existing encrypt/key-source tests now assert header transport; link-user behavior
is unchanged. Focused race tests and affected package suites are run locally;
hosted CI is the complete repository check. Named mutants remove the document,
per-call scope, App branch, mode check, no-retry, scrub and success guard.

QA must run the compiled CLI on a private stack with server#1796 step 1, encrypt
with each flag, then --unlock read-back for the passphrase case. Author checks use
fake HTTP only. The coordinator must verify step 1 is live in production before a
CLI release; the server's argument-removal slice waits for that release. This PR
uses Refs #1650 and leaves issue closure/release sequencing to the coordinator.
