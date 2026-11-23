package team

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

// cli#2422 (child of #1617; hadron-server#3323, merged 2026-11-04 by #3376,
// cor:dmo:050:12): --instruction <kind>:<ref> on the twelve `team task` writes.
// The claim rides only the mutation, in its own document (agenttask_instruction_
// 2422.graphql), so a no-flag request is what it always was. The worker session
// attaches exactly as it does without the flag (AuthoredWriteContext verifies
// the binding for every authored write); this adds only the claim, and the
// server decides admission. A refusal is shown once and never retried without it.

// taskInstructionFloor is the task writes' shared server floor (hadron-server#3323).
const taskInstructionFloor = instruction.ServerFloorInstructionManagerWrites

const taskInstructionFlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server " + taskInstructionFloor + " or later"

// taskInstructionHelp is the paragraph every instructed task write shares.
const taskInstructionHelp = `--instruction <kind>:<ref> claims, for this one call, that the worker's holder
told it to make this change. kind is team-chat-message, task or live-chat; ref
is a reference to where the instruction was given (a team-chat message URN, a
task URN, or an opaque client address), never its text. The CLI splits on the
first colon only and sends the ref as written; a bad kind or a blank ref is
refused (exit 2) before any request. The claim rides this one write: the
worker session attaches as it does without the flag, and any lookup the
command makes first carries no claim. The server checks the reference and
decides whether the claim is admitted; its refusal is shown once and the call
is never retried without the flag. The flag needs server ` + taskInstructionFloor + ` or later;
an older server's refusal is passed through. The MCP tools take no
instruction, so this is the way to make the call from a worktree.`

// taskInstructionHelpShort is the short form for pages where the flag is a side
// note: what it is, who leaves it out, the minimum server, and where the full
// rules are. floor is the server floor the command's instructed path needs.
func taskInstructionHelpShort(floor string) string {
	return `--instruction <kind>:<ref> is for an AI worker acting on its holder's say-so
(the holder is the person responsible for the worker). kind is
team-chat-message, task or live-chat; ref says where the holder gave it (a
team-chat message URN, a task URN, or a client address), never its text. A
person, or a worker acting on its own authority, leaves it out. It needs server
` + floor + ` or later; an older server refuses the flag. The full rules are in
"hadron agentic-usage".`
}

func addTaskInstructionFlag(cmd *cobra.Command, raw *string) {
	cmd.Flags().StringVar(raw, "instruction", "", taskInstructionFlagUsage)
}

// taskInstructionFlagUsageFor renders the shared task-write instruction flag
// line for a command whose instructed path needs the server floor `floor`
// instead of taskInstructionFloor (cli#2504). It panics when the shared text no
// longer names taskInstructionFloor: a silent no-op replace would print the
// wrong minimum server with no test failing, and the panic fires while the
// command tree is built, so every test run sees it.
func taskInstructionFlagUsageFor(floor string) string {
	if !strings.Contains(taskInstructionFlagUsage, taskInstructionFloor) {
		panic("taskInstructionFlagUsageFor: the shared task instruction flag usage no longer names its floor")
	}
	return strings.ReplaceAll(taskInstructionFlagUsage, taskInstructionFloor, floor)
}

// taskClaim reads --instruction: nil when the flag is absent (the command then
// sends its plain document), else the parsed claim, or a usage refusal before
// any request is made.
func taskClaim(cmd *cobra.Command, raw string) (*gen.ActInstructionInput, error) {
	if !cmd.Flags().Changed("instruction") {
		return nil, nil
	}
	claim, err := instruction.Parse(raw)
	if err != nil {
		return nil, err
	}
	return claim, nil
}

// errNoTaskInstructionForBatch is the refusal for `create --batch-file
// --instruction`: createAgentTasks has no instruction argument.
var errNoTaskInstructionForBatch = exitcode.Newf(exitcode.Usage, "--instruction does not combine with --batch-file: the batch write (createAgentTasks) takes no instruction; create the tasks one at a time to claim a holder's instruction. Nothing was sent")
