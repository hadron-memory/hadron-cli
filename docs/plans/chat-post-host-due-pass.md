# C2: post-success host due-delivery pass (#908)

CHECKED gate6257700171, dispatch21514. This branch implements the command lifecycle behind a typed preparation/pass seam. It is **not activated**: the production constructor injects no adapter and exposes no --orca-host-config flag until C1 and S1/S5 have landed and a real adapter is integrated.

The command's injected constructor validates an explicitly empty flag before any post. Preparation receives the posting App and deployment before CreateTeamChatMessage; its future C1 adapter must validate config/key/current registration and canonical target identity without registering a host. No opt-in means preparation never runs. Confirmed post output precedes the pass; failed post/output runs no pass, and output failure warns that the post nevertheless succeeded. Pass failure/holds retain post exit0 and output, with fixed sanitized recovery diagnostics, never another post.

The future adapter consumes the shared C1 internal/delivery + internal/orcaterm packages and delivery.graphql operations. It owns pagination (including empty batch pages with continuation), current host scope, separate session-free key, terminal locks/confirmation/receipt recovery and S5 holds. The pass includes already-due older unrelated batches on this host. This branch deliberately does not duplicate those mechanisms or generate speculative server operations.

Tests execute the actual post command against a local GraphQL fixture, pin preflight-before-post and output-before-pass, no opt-in access, empty flag, preflight/post failure, pass failure/held exit0, secret-free diagnostics and receipt-write failure. They are command/typed-seam evidence, **not** real C1 terminal/server authority, pagination/concurrency or compiled-CLI acceptance. Those tests, integration, codegen, explicit production flag/help wiring and independent T2+§10 review/QA remain blocked on landed dependencies. No Closes #908 until the full slice is delivered.

## C1 integration pin

Joshua pinned the C2 surface at cli#908 comment 6257749562, C1 branch
joshua/907-host-delivery ee258224. After it lands, prepare via
hostpoller.LoadConfig/LoadState/CheckWorktreeAliases, cfg.RefFor and
cfg.Recipient; construct orcaterm.NewClient with the configured Orca binary
and CallTimeout. Use delivery.New(server,client,cfg.DeliveryStateDir()).Deliver
for each worker transaction. Deliver owns the shared lock and returns
skipped/locked when the poller holds it; never add WithWorkerLock around it.
The shared S1/S5 adapter is planned as cmd/team/delivery_s1.go,
newDeliveryServer plus Batches(ctx,appRef,workerRefs). No feature-branch
implementation is imported by this staging PR.
