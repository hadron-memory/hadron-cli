# Instructed availability writes (cli#2450)

`team availability set --instruction <kind>:<ref>` supports both worker and
model-family records. The optional claim uses separate generated documents;
without the flag, the original documents, inputs and receipt JSON remain
unchanged. A worker record retains authored attribution without the flag;
a family record remains the signed-in person's write without it.

Existing availability usage checks run first, then the claim parser, then a
binding snapshot before lookups. The shared instructed-write helper checks the
same server, target App and unchanged binding before attaching a session. A
direct worker id/URN with no App scope uses the server-target precedent from
#2457: the server resolves and authorizes the target; the CLI invents no App.
An unusable or changed binding follows `instructedAsPerson`, keeping the claim
but attaching no session. Explicit instructions suppress automatic authored
attribution so it cannot attach a different session to this fallback.

The server decides admission, including the manager's held worker on the
family arm and a worker's own arm. One mutation is sent; refusals retain their
code and message and never trigger a plain retry. A missing instructed result
is an unknown outcome: read availability before retrying.

Requires server `ba077810` (hadron-server#3376), cor:dmo:050:12 and
cor:agt:060:08/:09. No server change, other instruction family or release is
included. Fake-server tests prove client requests and receipts only; the
independent QA seat runs a compiled CLI and §10 reviews attribution at every
head.
