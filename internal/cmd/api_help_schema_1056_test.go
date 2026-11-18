package cmd

import (
	"strings"
	"testing"
)

// cli#1056 (help audit, Gil's step 1 at e5f1c740): `hadron api --help` sent
// users to a '__schema' introspection query to learn the field names, but the
// hosted default server refuses introspection (HTTP 400,
// INTROSPECTION_DISABLED, exit 2), and the fallback was a repository path an
// installed CLI's user does not have. The page must say introspection is
// refused there and name a schema source a user can open.
func TestAPIHelpNamesAReachableSchema1056(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"api"})
	if err != nil {
		t.Fatalf("find api: %v", err)
	}
	long := strings.Join(strings.Fields(cmd.Long), " ")

	const schemaURL = "https://github.com/hadron-memory/hadron-cli/blob/main/schema/schema.graphql"
	if !strings.Contains(long, schemaURL) {
		t.Errorf("api help must link the published schema %s; got:\n%s", schemaURL, cmd.Long)
	}
	// "refuse" appears elsewhere on the page ('"-" is refused'), so look only at
	// the sentence that offers the '__schema' query.
	for _, s := range strings.Split(long, ". ") {
		if strings.Contains(s, "__schema") && !strings.Contains(s, "refuse") {
			t.Errorf("api help offers a '__schema' query without saying, in that sentence, that the default server refuses it: %q", s)
		}
	}
	if strings.Contains(long, "read schema/schema.graphql in the hadron-cli repository") {
		t.Errorf("api help still points at a repository path instead of a link:\n%s", cmd.Long)
	}
}
