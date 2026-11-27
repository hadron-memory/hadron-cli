package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// registerDTO is the App register's answer to an event code, as the server
// gave it (cor:dmo:050:24: shown so a caller can see why a call did or did not
// decide). Null when no event code was given.
type registerDTO struct {
	Code    string  `json:"code"`
	State   string  `json:"state"`
	TaskLoc *string `json:"taskLoc"`
	TaskURN *string `json:"taskUrn"`
}

// instructionsDTO is the whole `agentInstructions` answer. Lists are never
// null; absent singletons are explicit nulls so the shape never changes.
type instructionsDTO struct {
	State        string          `json:"state"`
	Route        *string         `json:"route"`
	Instructions *string         `json:"instructions"`
	Candidates   []phraseTaskDTO `json:"candidates"`
	Nearest      []phraseTaskDTO `json:"nearest"`
	Fallback     *phraseTaskDTO  `json:"fallback"`
	Register     *registerDTO    `json:"register"`
}

func instructionsTaskDTO(t *gen.InstructionsListedTask) phraseTaskDTO {
	dto := phraseTaskDTO{Trigger: t.Trigger, Score: t.Score}
	if t.Node != nil {
		dto.Node = listedNodeDTO{ID: t.Node.Id, URN: t.Node.Urn, Name: t.Node.Name}
		if in := t.Node.InheritedFrom; in != nil {
			dto.Node.InheritedFrom = &inheritedFromDTO{AgentID: in.AgentId, AgentUrn: in.AgentUrn}
		}
	}
	if t.MatchedBy != nil {
		m := string(*t.MatchedBy)
		dto.MatchedBy = &m
	}
	return dto
}

func newCmdAgentInstructions(f *cmdutil.Factory) *cobra.Command {
	var (
		phraseFlag    string
		eventCodeFlag string
		agentFlag     string
	)
	cmd := &cobra.Command{
		Use:   `instructions ["<phrase>"] [--event-code CODE] [--agent REF] [--app REF]`,
		Short: "Ask what to do now: the best-fit role task and its body",
		Long: `Ask "what do I do now?" and get the role task to follow. The server picks the
task; this command only sends what you gave it and shows the answer.

Give a phrase (the positional argument or --phrase: what is happening, in your
own words), an event code (--event-code, an App's event such as a wake), or
both. With an event code, the App's register answers first: a task it maps is
the match, route REGISTER. Otherwise the phrase is ranked against the Agent's
tasks on their abstracts and the best task at or above the server's no-match
floor is the match, route SEARCH.

A match prints the task's name, URN, route and score, then the task's body.
Any runners-up are named below it as "not instructions": they never get a body.
No match prints "No task fits", the nearest tasks (marked not instructions, for
diagnosis) and the fallback task if the App's register names one. Without an
embedding the server cannot score: such a task shows "no score", never 0.
When an event code was given, the register's answer is printed too, so you can
see why the call did or did not decide.

Which Agent: --agent names it. A bound worker may leave it out and the server
uses the worker's own Agent (this command never looks one up); a caller with no
Agent of its own gets the server's refusal.

Which App: the call is App-less unless you name one. --app is sent when given;
with --event-code the register needs an App, so the configured App is sent
then. A phrase-only call never sends the configured App: an App-scoped call is
refused when the Agent is not installed there, where an App-less one answers.
An event code with no App is refused by the server, not checked here.

Nothing is kept: the phrase and event code are sent to the server, which does
not log them, and this command writes no history, log or cache of them.

--json prints the whole answer: state, route, instructions, candidates (with
scores), nearest, fallback and register.

Needs a server from 2026-11-27 or later; an older server refuses the call.`,
		Example: `  hadron agent instructions "the build is red on windows" --agent hrn:agent:acme.com:support-bot
  hadron agent instructions "the nightly job failed"
  hadron agent instructions --event-code build.failed --app hrn:app:acme.com:team
  hadron agent instructions "what now" --agent hrn:agent:acme.com:support-bot --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			var phrase *string
			switch {
			case len(args) == 1 && flags.Changed("phrase"):
				return exitcode.Newf(exitcode.Usage, "give the phrase once: either as the argument or with --phrase, not both")
			case len(args) == 1:
				phrase = &args[0]
			case flags.Changed("phrase"):
				phrase = &phraseFlag
			}
			var eventCode *string
			if flags.Changed("event-code") {
				eventCode = &eventCodeFlag
			}
			if phrase == nil && eventCode == nil {
				return exitcode.Newf(exitcode.Usage, "give a phrase (an argument or --phrase), an --event-code, or both")
			}
			var agentRef *string
			if flags.Changed("agent") {
				if strings.TrimSpace(agentFlag) == "" {
					return exitcode.Newf(exitcode.Usage, "--agent was given a blank value — name the Agent, or omit --agent")
				}
				agentRef = &agentFlag
			}
			// The App is sent only when given, or when the register arm needs
			// one (an event code). A phrase-only call stays App-less.
			var appRef *string
			appNote := ""
			if fl := cmd.Flag("app"); fl != nil && fl.Changed {
				if strings.TrimSpace(f.AppFlag) == "" {
					return exitcode.Newf(exitcode.Usage, "--app was given a blank value — name the App, or omit --app")
				}
				a, err := cmdutil.CanonicalAppRef("--app", f.AppFlag)
				if err != nil {
					return err
				}
				appRef = &a
				appNote = a + " (from --app)"
			} else if eventCode != nil {
				a, err := f.App()
				if err != nil {
					return err
				}
				if a != "" {
					appRef = &a
					appNote = a + " (the configured App, sent because of --event-code)"
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentInstructions(cmd.Context(), client, agentRef, appRef, eventCode, phrase)
			if err != nil {
				return api.MapError(err)
			}
			dto, err := instructionsAnswer(resp.AgentInstructions)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeInstructions(w, dto, appNote)
			})
		},
	}
	cmd.Flags().StringVar(&phraseFlag, "phrase", "", "what is happening, in your words (or give it as the argument)")
	cmd.Flags().StringVar(&eventCodeFlag, "event-code", "", "an App event code the App's register maps to a task (needs an App: --app or the configured one)")
	cmd.Flags().StringVar(&agentFlag, "agent", "", "the Agent to ask (a bound worker may omit it: the server uses the worker's own Agent)")
	return cmd
}

// instructionsAnswer maps the server's answer to the DTO, refusing a malformed
// one (nothing printed): a MATCHED with no candidate or no body would otherwise
// print a task that is not there.
func instructionsAnswer(a *gen.AgentInstructionsAgentInstructions) (instructionsDTO, error) {
	if a == nil {
		return instructionsDTO{}, exitcode.Newf(exitcode.Error, "the server returned no answer; nothing to print")
	}
	dto := instructionsDTO{
		State:        string(a.State),
		Instructions: a.Instructions,
		Candidates:   []phraseTaskDTO{},
		Nearest:      []phraseTaskDTO{},
	}
	if a.Route != nil {
		r := string(*a.Route)
		dto.Route = &r
	}
	for _, c := range a.Candidates {
		if c != nil {
			dto.Candidates = append(dto.Candidates, instructionsTaskDTO(&c.InstructionsListedTask))
		}
	}
	for _, n := range a.Nearest {
		if n != nil {
			dto.Nearest = append(dto.Nearest, instructionsTaskDTO(&n.InstructionsListedTask))
		}
	}
	if a.Fallback != nil {
		fb := instructionsTaskDTO(&a.Fallback.InstructionsListedTask)
		dto.Fallback = &fb
	}
	if r := a.Register; r != nil {
		dto.Register = &registerDTO{Code: r.Code, State: string(r.State), TaskLoc: r.TaskLoc, TaskURN: r.TaskUrn}
	}
	switch a.State {
	case gen.AgentInstructionsStateMatched:
		if len(dto.Candidates) == 0 || dto.Instructions == nil {
			return dto, exitcode.Newf(exitcode.Error, "the server answered MATCHED without a task and its body; nothing to print")
		}
	case gen.AgentInstructionsStateNoMatch:
		// A no-match is never instructions, whatever else the server sent.
		dto.Instructions = nil
		dto.Candidates = []phraseTaskDTO{}
	default:
		return dto, exitcode.Newf(exitcode.Error, "the server answered an unknown state %q; nothing to print", a.State)
	}
	return dto, nil
}

func writeInstructions(w io.Writer, a instructionsDTO, appNote string) error {
	if appNote != "" {
		fmt.Fprintf(w, "App:    %s\n", output.SafeCell(appNote))
	}
	if a.State == string(gen.AgentInstructionsStateMatched) {
		best := a.Candidates[0]
		fmt.Fprintf(w, "Task:   %s\n", output.SafeCell(best.Node.Name))
		fmt.Fprintf(w, "URN:    %s\n", output.SafeCell(best.Node.URN))
		route := ""
		if a.Route != nil {
			route = *a.Route
		}
		fmt.Fprintf(w, "Route:  %s\n", route)
		fmt.Fprintf(w, "Score:  %s\n", scoreCell(best.Score))
		writeRegister(w, a.Register)
		fmt.Fprintf(w, "\n%s\n", *a.Instructions)
		if len(a.Candidates) > 1 {
			fmt.Fprintln(w, "\nAlso considered (not instructions):")
			if err := writeTaskTable(w, a.Candidates[1:]); err != nil {
				return err
			}
		}
		return nil
	}
	fmt.Fprintln(w, "No task fits.")
	writeRegister(w, a.Register)
	if len(a.Nearest) > 0 {
		fmt.Fprintln(w, "\nNearest tasks (not instructions):")
		if err := writeTaskTable(w, a.Nearest); err != nil {
			return err
		}
	}
	if a.Fallback != nil {
		fmt.Fprintf(w, "\nFallback task (the App's register names it for no match): %s  %s\n", output.SafeCell(a.Fallback.Node.Name), output.SafeCell(a.Fallback.Node.URN))
	}
	return nil
}

func writeRegister(w io.Writer, r *registerDTO) {
	if r == nil {
		return
	}
	line := fmt.Sprintf("Register: %s %s", output.SafeCell(r.Code), r.State)
	if r.TaskURN != nil {
		line += " -> " + output.SafeCell(*r.TaskURN)
	} else if r.TaskLoc != nil {
		line += " (loc " + output.SafeCell(*r.TaskLoc) + ")"
	}
	fmt.Fprintln(w, line)
}

func writeTaskTable(w io.Writer, rows []phraseTaskDTO) error {
	t := output.NewTable(w, "NAME", "URN", "SCORE", "MATCHED BY")
	for _, row := range rows {
		matched := ""
		if row.MatchedBy != nil {
			matched = *row.MatchedBy
		}
		t.Row(output.SafeCell(row.Node.Name), output.SafeCell(row.Node.URN), scoreCell(row.Score), output.SafeCell(matched))
	}
	return t.Flush()
}
