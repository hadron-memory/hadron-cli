package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1056 (help audit, step-1 evaluation on the issue): `hadron api --help`
// sent users to a '__schema' introspection query to learn the field names,
// but the hosted default server refuses introspection (HTTP 400,
// INTROSPECTION_DISABLED, exit 2), and the fallback was a repository path an
// installed CLI's user does not have. The page must say introspection is
// refused there and link a schema source a user can open.

// schemaPath is the repository file the help links on GitHub's main branch.
const schemaPath = "schema/schema.graphql"

const schemaURL = "https://github.com/hadron-memory/hadron-cli/blob/main/" + schemaPath

func apiLong(t *testing.T) string {
	t.Helper()
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"api"})
	if err != nil {
		t.Fatalf("find api: %v", err)
	}
	return cmd.Long
}

func TestAPIHelpNamesAReachableSchema1056(t *testing.T) {
	long := apiLong(t)
	flat := strings.Join(strings.Fields(long), " ")

	// The page says, positively, that the default server refuses
	// introspection, naming both introspection fields.
	for _, want := range []string{"The hosted server (the default) refuses introspection", "'__schema'", "'__type'"} {
		if !strings.Contains(flat, want) {
			t.Errorf("api help must say %q; got:\n%s", want, long)
		}
	}

	// The link is printed whole on one line, so a terminal shows it unbroken.
	onOneLine := false
	for _, line := range strings.Split(long, "\n") {
		if strings.TrimSpace(line) == schemaURL {
			onOneLine = true
		}
	}
	if !onOneLine {
		t.Errorf("api help must print %s on a line of its own; got:\n%s", schemaURL, long)
	}

	if strings.Contains(flat, "in the hadron-cli repository") {
		t.Errorf("api help still points at a repository path instead of a link:\n%s", long)
	}
}

// The linked file must exist at that path in this repository, so moving or
// renaming it fails here instead of leaving the help pointing at a 404.
func TestAPIHelpSchemaLinkTargetExists1056(t *testing.T) {
	info, err := os.Stat(filepath.Join("..", "..", schemaPath))
	if err != nil {
		t.Fatalf("the api help links %s, but %s is missing: %v", schemaURL, schemaPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("%s is empty", schemaPath)
	}
}
