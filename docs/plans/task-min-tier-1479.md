# Task minimum model tier (cli#1479)

The CLI forwards `--min-tier low|mid|high` in task requirements. It validates
only the exact token syntax; matching and authorization remain server-owned.
The server prefers the lowest sufficient tier and excludes workers of unknown
tier when a minimum is set (cor:agt:060:06 r21; cor:agt:060:04 r22).
There is no maximum-tier flag or client-side eligibility calculation.

Create omits minTier when absent, including when other requirements are set.
Update replaces the whole requirements object whenever any requirement flag is
named; callers must repeat requirements they want to keep. Clear-requirements
sends null and clears the tier too. Empty/invalid tier tokens and tier plus
clear-requirements refuse locally with exit 2 before HTTP. Server refusals keep
the existing task error mapping and message.

Task get/list use the stable requirements DTO: minTier is a string or explicit
null when requirements exists. Human get prints it only when set; list adds a
MIN TIER column only if a row has a tier, leaving absent rows blank. Human
values pass through SafeCell; JSON preserves server values.

Schema source: hadron-server 31d638284211a972719abd84baf5af6bd370b102 (#2983).
The exporter ran in Jane's isolated detached worktree, with a private minimal
exporter dependency install (the repository-wide npm install failed). The SDL
diff adds only AgentTaskRequirements.minTier and its input counterpart: no
unrelated drift. Generated input minTier uses omitempty. All task operations
selecting the shared fragment now need a server containing #2983; deployment
is a production-use prerequisite, not something this build claims to perform.

Red commit 8157191d pins create/update wire values, replacement, clear,
pre-request refusals, server BAD_USER_INPUT and get/list human/JSON output.
Gate-required mutants: drop create's tier, remove clear conflict, omit JSON null.
Independent LOCAL/BLIND and synthetic CLI QA remain required at frozen head.
