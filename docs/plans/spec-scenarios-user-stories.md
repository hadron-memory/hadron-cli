# Optional scenarios / user-stories section in the rule rubric (#217)

> Review artifact for the PR resolving
> [#217](https://github.com/hadron-memory/hadron-cli/issues/217) (evaluate
> Codex's product-spec guide, fold the useful parts into ours, add a
> scenarios/user-stories section, backfill the corpus). Bundled in the PR.

## Context

Issue #217 supplies a "Lightweight Spec Writing Guide" (from Codex) and asks us
to (1) compare it with our authoring instructions
(`hrn:node:hadronmemory.com::core::tasks:mint-spec`, the source of the
`add-spec` skill), (2) fold in what's missing, (3) add a **scenarios / user
stories** section — the owner "felt this would be helpful for humans to
understand the specs" — and (4) go through the `hadronmemory.com::specs` corpus
adding user stories where they help.

Mapping the Codex shape onto our rubric, everything lines up 1:1 (What It
Governs ≈ Definition, Core Behavior ≈ Rule & examples, plus Durable/Tunable,
What-Invalidates, Where-it-lives, Provenance) **except** two genuinely new
sections: *Scenarios / User Stories* and *Acceptance Criteria*. Codex's own
framing is deliberately anti-ceremony ("keep it small, 3–7", "do not add stories
just to fill a template") — which drives the enforcement decision below.

## Decisions (agreed with the owner)

- **Scenarios: optional, un-linted.** Making them mandatory would flag the whole
  existing corpus and add exactly the ceremony Codex warns against. `lint` still
  hard-enforces only the abstract and the *What invalidates* statement.
- **Placement: after the Definition, before Rule & examples** (Codex order) —
  intent frames the reader before the precise contract.
- **Acceptance Criteria: also added, optional** — for specs whose behavior must
  be testable.
- **Corpus backfill: actor-heavy modules + judgment** — auth / acl / api / cht /
  int and anywhere stories clarify intent; skip self-evident schema rules.
- Left alone: the second, partly-stale instruction node
  `specs::tasks:create-platform-spec` (separate follow-up).

## Change — CLI

`rubricBody` (`internal/cmd/spec/rubric.go`) now emits, **only at the rule tier
(Level 3)**, two clearly-optional sections an author fills in or deletes:

| Section | Where | Placeholder guidance |
|---|---|---|
| `## Scenarios / user stories` *(optional)* | after Definition | 3–7 `As a <actor>, I want <capability>, so that <outcome>.` (or plain `Scenarios:` bullets); happy path + alternates + identity/permission boundaries |
| `## Acceptance criteria` *(optional)* | after What-invalidates | concrete checkable bullets, when behavior must be testable |

Flows (Level 4) stay terse — they inherit their rule's scenarios and are pulled
on demand, so they scaffold only the mandatory rubric. Two new heading constants
(`headingScenarios`, `headingAcceptance`); no lint, flag, schema, or wire
changes — the sections are un-linted by design, so the existing
`TestScaffoldPassesStructuralLint` still holds.

**Tests.** `rubric_test.go`: the `rule` case now asserts both optional headings
and the `As a <actor>` stub; a new `TestOptionalSectionsRuleTierOnly` pins the
ordering (definition → scenarios → rule) and that flows omit them.

## Change — docs & instructions

- `docs/how-to/maintain-product-specs.md` gains a "The rule rubric" section
  listing the mandatory vs optional sections.
- `core::tasks:mint-spec` (Hadron memory): the "Write the body" list gains the
  two optional sections plus a "Scenarios / user stories" subsection covering the
  user-story form, the scenarios-vs-stories distinction, the 3–7 guidance, and
  that scenarios are un-linted.

## Change — corpus

User stories backfilled into the actor-heavy specs in
`hadronmemory.com::specs` (auth / acl / api / cht / int) where they clarify
intent, using the same `As a <actor>, …` / `Scenarios:` forms.
