package team

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

// cli#2422 (child of #1617; hadron-server#3323, merged as ba077810 by #3376,
// cor:dmo:050:12): --instruction <kind>:<ref> on the twelve `team task` writes.
// The claim rides only the mutation, in its own document (agenttask_instruction_
// 2422.graphql), so a no-flag request is what it always was. The worker session
// attaches exactly as it does without the flag (AuthoredWriteContext verifies
// the binding for every authored write); this adds only the claim, and the
// server decides admission. A refusal is shown once and never retried without it.

const taskInstructionFlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server ba077810 or later"

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
is never retried without the flag. The flag needs server ba077810 or later;
an older server's refusal is passed through. The MCP tools take no
instruction, so this is the way to make the call from a worktree.`

func addTaskInstructionFlag(cmd *cobra.Command, raw *string) {
	cmd.Flags().StringVar(raw, "instruction", "", taskInstructionFlagUsage)
}

// taskInstructionHelpFor and taskInstructionFlagUsageFor render the shared
// task-write instruction help and flag line for a command whose instructed
// path needs server revision serverMin instead of ba077810 (cli#2504,
// mirroring instructionHelpFor for the non-task help). Like it, they panic
// when the shared text no longer names ba077810: a silent no-op replace would
// print the wrong minimum server with no test failing, and the panic fires
// while the command tree is built, so every test run sees it.
func taskInstructionHelpFor(serverMin string) string {
	if !strings.Contains(taskInstructionHelp, "ba077810") {
		panic("taskInstructionHelpFor: the shared task instruction help no longer names ba077810")
	}
	return strings.ReplaceAll(taskInstructionHelp, "ba077810", serverMin)
}

func taskInstructionFlagUsageFor(serverMin string) string {
	if !strings.Contains(taskInstructionFlagUsage, "ba077810") {
		panic("taskInstructionFlagUsageFor: the shared task instruction flag usage no longer names ba077810")
	}
	return strings.ReplaceAll(taskInstructionFlagUsage, "ba077810", serverMin)
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
