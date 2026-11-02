# Task matching companion (#1549)

Gate: Xanthos6587323247, acceptance6586269985. Refs #1549; server#2829/#2830
remain open until the coordinator reconciles the paired compiled-CLI QA leg.

Schema exported from a private detached server0356d7f12eedd11387c8e695735c8fea89c9bf8b
worktree. The refresh includes intervening main SDL declarations; generated
client changes trace to selected fields/new optional matching inputs. No
shared sibling schema is consumed. A server fix head with a different SDL
requires re-reading and regenerating before freeze.

Create sends match:true only for true; false and omission preserve existing
variables. The one server create mutation owns matching and all refusals.
Role lists parse comma-separated/repeated syntax; a single item retains role,
several use roles and a null single role (the server checks non-null values).
New input keys omit when absent. Team remains a raw server-resolved lead ref.
Client logic performs no eligibility/matching/authority computation.

MatchFailure DTO is optional; so are requirements.roles and teamWorkerId.
Old output bytes remain unchanged. Get/mutation receipts are bare task
objects and plain list is a bare array. Human reason/time and new requirement
values pass SafeCell. An unknown future reason is never discarded.
A created failed match is rendered before Silent(Conflict), so root prints no
second JSON envelope; writer errors return first. There is no retry path.

The shared fragment necessarily adds output selections for every task caller,
including those with no new flags. Input variables and request counts remain
unchanged; literal GraphQL documents change. All target providers must support
server3245; no CLI release before compatible deployment. Existing list --team
continues to read its coordinator view and rejects the new filter before reads.

Red first117bfba4 over main29c58a0b: unknown match flags, lost repeated roles,
empty role sent and metadata dropped. Synthetic tests cover flags, errors,
clear-mark reads, unknown reasons, silent exit/writer precedence and safety.
Real compiled CLI/raw GraphQL QA remains with Celine on her retained private
server3245 stack; author fake tests do not substitute for that leg.
