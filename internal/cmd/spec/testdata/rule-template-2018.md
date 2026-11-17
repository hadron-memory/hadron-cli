# The Rule spec template

> **Version 1.2.0 (2026-10-04).** Part of [[authoring:rule:author|Write a Hadron Rule spec]]. Instructions, not a task; maintained by the spec
> engineer. Changing a section here changes what `speccheck.py structure` expects: change the
> script's section list in [[authoring:common:scripts:speccheck|speccheck]] with it.

**Name:** `Rule: <plain title> [<citation>]`. **Role** `spec.rule`, no tags. Pass the whole name to
`hadron spec new --title`, with `--role spec.rule`.

**Built line:** required, clause by clause (rule 12 of the [[authoring:common:general-instructions|general instructions]]).

```markdown
# Rule Spec: <plain title>

- **Status:** Proposed | Decided (<who>, <date>, [<where it was decided>](<link>)) | Superseded by [[<citation>|<Type>: <title>]]
- **Built:** Built ([<repo>#<n>](<link>)) | Not built ([<repo>#<n>](<link>)) | Partly built: <clause> built ([<repo>#<n>](<link>)); <clause> not built ([<repo>#<n>](<link>))

<div class="spec-core" style="border-radius: 1em 1em 0 0; padding: 1em; background-color: gray;"></div>

## The Rule

**Applies to:** <who and where: which callers, surfaces, memories or kinds>

<One statement, two or three sentences at most, in plain words. "Always" and "never" do the job of
MUST and MUST NOT; no capitalised keywords.>

## Exceptions

- **<where the rule deliberately doesn't apply>.** <Why.> — or "None."

## What Stays Fixed

<What may not change without a new decision. The rule's own statement is always fixed.>

## What Can Change

<"Nothing in this rule." unless a ruling says a part may be tuned.>

<div class="spec-core" style="border-radius: 0 0 1em 1em; padding: 1em; background-color: gray;"></div>

## Why We Need This Rule

<What goes wrong without it, and for whom. Traced to the ruling, or marked Proposed.>

## Scenarios (How To Test)

- **S1.** Given …, when …, then ….
- **S2 (someone tries to break the rule).** Given …, when <someone tries what the rule forbids>,
  then <what they get instead>.

## Edge Cases

- **<case>.** <What happens.> — or "No edge cases known."

## Open Questions, Need Ruling

- **<question>?** <What the sources say, or where the question came from.> <Name> decides.
- (Or "None.")

---

## Decided and Set Aside

<Optional. A rejected alternative or a reversal: what, who decided, when, and the decision ID.
Leave the section out when there is none.>

## Where This Came From

- <Only links that exist: decision IDs as permalinks into the registry, chat messages as links to
  their nodes, issues, PRs, the Spec Kit folder. No commentary on the sources.>

## Links, References

- **Part of:** [[<citation>|Feature or Module: <title>]]
- **Uses:** [[<citation>|Definition, Entity or Rule: <title>]] — <one line each>
- **Related:** [[<citation>|<Type>: <title>]] — <one line each; leave the line out when there is none>
```

**The defining block** (The Rule, Exceptions, What Stays Fixed, What Can Change) sits between the two empty caps, copied
exactly. It is the Rule itself; everything outside it frames it, and nothing outside it may add to it.

**The horizontal rule** (`---`, a blank line on each side) goes directly above Decided and Set
Aside, or above Where This Came From when that section is left out. It is the only `---` in the body.

**Optional sections:** Decided and Set Aside. Every other section is required, in this order; leave an
optional section out rather than writing it empty.
