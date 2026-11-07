package cmd

import "fmt"

// Shared by reference-wire and existing compatibility fixtures.
func absentReferenceField1554(field string) string {
	return fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, `Cannot query field "`+field+`" on type "Query".`)
}
