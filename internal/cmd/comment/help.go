package comment

// Help text for the comment group. Every example and claim here was run
// against a live server (cli#1051 slice 2).

const refsHelp = `Name the node by its URN (hrn:node:<org>:<memory>:<loc>), its id, or a bare
loc (its path in the memory, such as onboarding or auth:tokens) with
-m/--memory. Name a comment by the URN or id that create, reply and list
print (hrn:node:<org>:<memory>:comments:<id>).`

const counterHelp = `--expected-revision is the comment's write counter: the number after "write
counter" in comment get or comment list (revSeq in --json). It is not the
node's revision. If the comment changed since you read it, the command exits 5
and prints the current value ("the comment is at revision 2, not 1"); read it
again and retry.`

const groupLong = `Leave feedback beside a note without changing it, answer other people's
feedback, and mark a discussion resolved when it is dealt with.

A comment is attached to one node. The first comment on it starts a thread;
replies join that thread; the thread stays OPEN until someone resolves it.
Each comment records the node revision it was written against, so a reader
can see when the note has changed since ("older target text"). Everyone who
can read the memory sees its comments here and in the portal. Writing a
comment needs write access to the memory. Comments are stored as nodes, but
only these commands can write them: node update and node rm refuse (exit 2).

` + refsHelp + `

` + counterHelp

const groupExample = `  hadron comment create onboarding -m hrn:mem:acme.com:kb --body "Should this mention make lint?"
  hadron comment list onboarding -m hrn:mem:acme.com:kb
  hadron comment reply hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --body "Yes, added."
  hadron comment resolve hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --expected-revision 1`

var verbLong = map[string]string{
	"create": `Start a comment thread on a node: feedback that sits beside the note without
changing it. Everyone who can read the memory sees it.

Give the text with --body "...", with --body - to read it from a pipe, or with
--body-file <path> for a Markdown file. One of them is required; the text is
at most 20,000 characters. --quote names the passage of the note the comment
is about (plain text, at most 2,000 characters); it is shown above the text.

The comment is attached to the node's current revision, or with
--anchor-revision N to an earlier one (1 up to the current revision, which
is the "revision:" line of hadron node get <node>).

You can have one open thread per node. If you already have one, the command
exits 5 and names it (threadId): reply to it, or resolve or delete it first.
A comment cannot be the target of another comment; use comment reply. Needs
write access to the memory.

` + refsHelp + `

On success it prints the new comment: its URN (the ref for reply, edit,
delete and resolve), OPEN, the author, write counter 1, the node revision it
is attached to, the quote and the text. --json prints the full record.`,

	"reply": `Answer a comment thread. Pass the URN or id of any comment in the thread, the
top-level comment or one of its replies; the reply always joins that thread,
after the replies already there (threads are one level deep). Resolved
threads take replies too and stay resolved. A thread whose top-level comment
was deleted takes no new replies (exit 5).

Give the text with --body "...", --body - (from a pipe) or --body-file <path>;
one is required. Needs write access to the memory.

On success it prints the new reply: its URN, "reply", the author and write
counter 1, then the text. --json prints the full record.`,

	"edit": `Change the text or the quote of a comment you wrote; nobody else can edit it.
Pass --body, --body - or --body-file for new text, --quote "..." for
a new quote, --quote "" to remove the quote, or text and quote together.
Whatever you leave out stays as it is. Comments keep no history: the old text
is replaced and cannot be restored.

` + counterHelp + `

On success it prints the comment with an "Edited:" line; its write counter
has gone up by one.`,

	"delete": `Delete a comment. Its text is removed permanently: comments keep no history,
so there is no undo. You may delete your own comments; a memory manager may
delete anyone's.

Deleting a top-level comment does not delete its replies. While the thread
still has replies, the top-level comment stays in it as a placeholder marked
"deleted", with no text; the placeholder goes when its last reply is deleted.

` + counterHelp + `

On a terminal it asks first ("Delete comment <ref>? Its text is removed and
cannot be restored. (y/N)"). Without a terminal (a script or an AI agent) pass
--yes; otherwise nothing is deleted and it exits 2. On success it prints the
comment marked "deleted".

--thread --reason <text> deletes the WHOLE thread the comment belongs to, as a
memory admin: every comment in it disappears from every read. Name any comment
of the thread. --reason is required and kept with the deletion for audit; no
--expected-revision is needed. It asks first on a terminal, and needs --yes
otherwise. Whether you may do it is the server's rule. On success it prints the
comment you named and that its thread was deleted.`,

	"resolve": `Mark a thread resolved once its feedback has been dealt with, or reopen it
with --reopen. Pass the thread's top-level comment (the one comment list
shows first); a reply is refused with exit 2. The comment's author or anyone
with write access to the memory may resolve or reopen it.

Nothing is deleted: a resolved thread still shows in comment list (filter
with --state) and still takes replies. Resolving a resolved thread, or
reopening an open one, exits 5. Reopening also exits 5 while the thread's
author has another open thread on the same node.

` + counterHelp + `

On success it prints the top-level comment with its new state, RESOLVED or
OPEN.`,

	"get": `Show one comment: its URN, its state (OPEN or RESOLVED for a top-level
comment, "reply" for a reply, "deleted"), the author, its write counter (what
edit, delete and resolve need for --expected-revision), the node revision it
is attached to and whether the node has changed since ("older target text"),
then the quote and the text.

A top-level comment deleted while it still had replies shows as "deleted"
with no text. A comment that is gone, or that you cannot read, exits 4.
--json prints the full record: revSeq is the write counter, target is the
node, threadRootId is the top-level comment's id (null on a top-level
comment), and body and quote are null once deleted.`,

	"list": `List the comment threads on a node. Each thread prints its top-level comment,
then its replies, oldest first. Every comment line gives its URN (the ref for
get, reply, edit, delete and resolve), its state, the author, its write
counter (for --expected-revision) and the node revision it was written
against; "older target text" means the node has changed since.

--state OPEN or --state RESOLVED shows only threads in that state (both,
comma-separated, is the same as leaving it out). --limit and --offset page
through threads: the first line, "Threads: N (showing M from offset O)", says
how many there are. A thread with more replies than fit says so on a
"Replies:" line.

` + refsHelp + `

--json prints {items, total}: each item is {root, replies, replyCount}.`,
}

var verbExample = map[string]string{
	"create": `  hadron comment create onboarding -m hrn:mem:acme.com:kb --body "Should this mention make lint?"
  hadron comment create hrn:node:acme.com:kb:onboarding --body "Out of date" --quote "Run make test before every push."
  hadron comment create onboarding -m hrn:mem:acme.com:kb --body-file review.md --anchor-revision 1`,
	"reply": `  hadron comment reply hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --body "Yes, lint too."
  echo "Fixed in revision 3." | hadron comment reply 01a18996fd187b77859b2190c6c51cc6 --body -`,
	"edit": `  hadron comment get hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6
  hadron comment edit hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --body "Should this mention make lint too?" --expected-revision 1
  hadron comment edit hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --quote "" --expected-revision 2`,
	"delete": `  hadron comment delete hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --expected-revision 3
  hadron comment delete 01a18996fd187b77859b2190c6c51cc6 --expected-revision 3 --yes --json
  hadron comment delete hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --thread --reason "spam" --yes`,
	"resolve": `  hadron comment resolve hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --expected-revision 3
  # resolving does not change the write counter, so reopening takes the same value
  hadron comment resolve hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6 --reopen --expected-revision 3`,
	"get": `  hadron comment get hrn:node:acme.com:kb:comments:01a18996fd187b77859b2190c6c51cc6
  hadron comment get 01a18996fd187b77859b2190c6c51cc6 --json`,
	"list": `  hadron comment list onboarding -m hrn:mem:acme.com:kb
  hadron comment list hrn:node:acme.com:kb:onboarding --state OPEN --json`,
}
