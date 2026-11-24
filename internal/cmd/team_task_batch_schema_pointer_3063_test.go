package cmd

// hadron-cli#3063 — `team task create --batch-file` says where the item's field
// names are listed: the GraphQL input types and the published schema. A reader
// who is not shown the names cannot write the file beyond the worked example.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const batchSchemaURL3063 = "https://github.com/hadron-memory/hadron-cli/blob/main/schema/schema.graphql"

// The help names both input types and the published schema, and says the server
// can have newer fields than that file.
func TestTaskCreateBatchHelpNamesTheSchemaTypes3063(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "team", "task", "create")), " ")
	for _, want := range []string{
		"CreateAgentTasksItemInput",
		"CreateAgentTaskInput",
		batchSchemaURL3063,
		"newer",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("team task create help lacks %q:\n%s", want, help)
		}
	}
}

// The pointer stays honest: both names are input types in the committed schema,
// and the item's `task` field is CreateAgentTaskInput.
func TestBatchSchemaTypesNamedInHelpExist3063(t *testing.T) {
	raw, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	sdl := string(raw)
	for _, name := range []string{"CreateAgentTasksItemInput", "CreateAgentTaskInput"} {
		if !regexp.MustCompile(`(?m)^input ` + name + ` \{`).MatchString(sdl) {
			t.Errorf("schema.graphql has no `input %s`", name)
		}
	}
	item := regexp.MustCompile(`(?s)input CreateAgentTasksItemInput \{(.*?)\n\}`).FindStringSubmatch(sdl)
	if item == nil {
		t.Fatal("CreateAgentTasksItemInput body not found")
	}
	if !regexp.MustCompile(`(?m)^\s*task:\s*CreateAgentTaskInput!`).MatchString(item[1]) {
		t.Errorf("CreateAgentTasksItemInput.task is not CreateAgentTaskInput!:\n%s", item[1])
	}
}
