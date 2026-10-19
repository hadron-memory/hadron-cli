package orcaterm

// Verbatim screen tails from router.test.ts at hadron-server b8384631
// (converted mechanically, bytes preserved), plus the shared shapes the
// teamrouter suite already pins.

const rule = "────────────────────────────────────────────────────────────"
const claudeHint = "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"

var claudeIdle = []string{"⏺ Done.", "❯ an earlier submitted turn", "✻ Cogitated for 1m 3s", rule, "❯ ", rule, claudeHint}
var codexIdle = []string{"  Worked for 33s • 16:32", "› Ask Codex to do anything", "  GPT-6-Sol high · ~/orca/workspaces/hadron-server/andi · Start worker session as Andi", "  ? for shortcuts"}
var claudeBusyEmpty = []string{"  ⎿ Tip: Use /btw to ask a quick side question", "  (ctrl+b to run in background)", "✽ Bunning… (5m 55s · ↓ 15.6k tokens)", rule, "❯ ", rule, claudeHint}
var codexBusyEmpty = []string{"  • Working (6s • esc to interrupt)", "› Ask Codex to do anything", "  GPT-6-Sol high · ~/orca/workspaces/hadron-server/andi · Start worker session as Andi", "  ? for shortcuts"}

var codexIdleRecapDiego = []string{
	"    + 1 line (^t to expand)",
	"  Worked for 10s • 3:49 AM",
	"  ↳ Recap: Completed and posted the assigned blind reviews and carries,",
	"            including #1899’s race fix, #1929, #1923, cli#883, and latest",
	"            #1930/#1959 approvals. Reviews were static; local test execution is",
	"            not reported. The router classifier fix is merged but not yet",
	"            reloaded. Chat is confirmed read through #12203; the latest unread",
	"            batch remains pending.",
	"            Next: Read unfiltered team chat from #12204 using the live session,",
	"            handle dispatches, and mark read; finish silently if no action is",
	"            needed.",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-portal/diego · Review hadron-…",
	"  ? for shortcuts",
}

var codexIdleRecapGene = []string{
	"  Worked for 24s • 7:38 PM",
	"  ↳ Recap: Gene completed the comment API migration in PR #1070 after #1059/",
	"            #1060 merged, then pushed hint and history-link fixes with",
	"            regression tests. The migration passed full validation; follow-ups",
	"            passed targeted tests and typecheck. Latest review/deployment",
	"            status is unconfirmed. Current task is reading unread chat from",
	"            #9605; completion and any assignments remain unconfirmed. Keep",
	"            Gene’s session open.",
	"            Next: Read chat unfiltered from #9605 using Gene’s existing",
	"            session, act on assignments, then mark read; finish silently if",
	"            none.",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-portal/gene · Bind Gene worker…",
	"  ? for shortcuts",
}

var codexIdleRecapXavier = []string{
	"    + 2 lines (^t to expand)",
	"• Read unfiltered and marked through #11441. Replied to Vera’s Built-line",
	"  survey as Xavier in #11442.",
	"  Worked for 1m 4s • 4:04 PM",
	"  ↳ Recap: The task is to read Hadron team chat unfiltered, handle actionable",
	"            mentions, and mark it read. Latest backlog was read and marked",
	"            through #11441; Xavier replied to Vera’s Built-line survey in",
	"            #11442. #1842 reached CHECKED, T2 + §10 and was posted to Eden.",
	"            #1770 was dispatched but remains in review under Xylon’s gate.",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/xavier · Verify Xavier…",
	"  ? for shortcuts",
}

var codexIdleUrlReceipt = []string{
	"  Reviewer approved: https://github.com/hadron-memory/hadron-portal/pull/",
	"  1118#issuecomment-6182034412",
	"  Receipt posted: https://github.com/hadron-memory/hadron-server/pull/1932#",
	"  issuecomment-6180470118",
	"    + 3 lines (^t to expand)",
	"  Worked for 1m 28s • 7:41 AM",
	"  ↳ Recap: Seat verdict saved and read back before the signed receipt was",
	"            posted; the private stack is cleaned and the binding stays live.",
	"            Chat was marked read through the receipt; nothing else was owed.",
	"            Next: available for routing.",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/atilla · Static review…",
	"  ? for shortcuts",
}

var codexUnknownIndentNoMarker = []string{
	"            deeply indented continuation-shaped output with no completed-",
	"            turn footer anywhere on screen",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/riana · Start worker session…",
	"  ? for shortcuts",
}

var codexIdleNoticeRiana = []string{
	"• Ran hadron node create --help; ls /tmp/riana*1883* /tmp/riana*1877* | head -20",
	"    + 109 lines (^t to expand)",
	"  ⓘ This content can’t be shown",
	"  We take extra care with some cybersecurity requests. If you’re doing",
	"  authorized security work, apply for Daybreak to get broader access.",
	"  Learn more: https://help.openai.com/en/articles/20001326",
	"  Apply for Daybreak: https://openai.com/form/enterprise-trusted-access-for-cyber/",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/riana · Start worker session…",
	"  ? for shortcuts",
}

var codexIdleNoticeChip = []string{
	"  └ (no output)",
	"    + Show details",
	"  ⓘ This content can’t be shown",
	"  We take extra care with some cybersecurity requests. If you’re doing authorized security work, apply for Daybreak to",
	"  get broader access.",
	"  Learn more: https://help.openai.com/en/articles/20001326",
	"  Apply for Daybreak: https://openai.com/form/enterprise-trusted-access-for-cyber/",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/chip-t1 · Catch up Chip QA session",
	"  ? for shortcuts",
}

var codexUnknownIndentedStatusNoHeader = []string{
	"  Worked for 1m 28s • 7:41 AM",
	"          an indented current status nobody classified",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
	"  ? for shortcuts",
}

var codexUnknownStatusNoRecap = []string{
	"  Worked for 1m 28s • 7:41 AM",
	"  an unindented current status nobody classified",
	"› Ask Codex to do anything",
	"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
	"  ? for shortcuts",
}

var claudeIdleRecapYuri = []string{
	"  #1958 progress: shed node done and corrected; rotate, start-session (both",
	"  tracks), and end-session nodes next — same ladders, rotation keeps end",
	"  --resume-claim since its successor starts fresh by design.",
	"✻ Worked for 1m 10s · done 3:54 PM",
	"  ※ recap: Executing my half of #1958: rewriting the lifecycle task nodes to use",
	"    resume claims. The shed node is done and corrected; next action is applying",
	"    the same claim ladders to the rotate and start/end-session nodes.",
	rule,
	"❯ ",
	rule,
	claudeHint,
}

var claudeUnknownCodeblockNoHeader = []string{
	"✻ Worked for 2m 6s · done 3:58 PM",
	"    const verdict = computeLiveness(row);",
	"    return verdict;",
	rule,
	"❯ ",
	rule,
	claudeHint,
}
