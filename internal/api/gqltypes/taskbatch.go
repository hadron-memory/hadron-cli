package gqltypes

import "encoding/json"

// CreateAgentTasksItemInput is one task of a createAgentTasks call
// (server#2653, cor:agt:050:06, cli#1565).
//
// Task is the item's CreateAgentTaskInput exactly as the batch file spelled it.
// It stays raw on purpose: the generated CreateAgentTaskInput marshals an
// absent optional field (description, source, startBy, status, tags, …) as an
// explicit null, and a batch item must keep an omitted key omitted — null reads
// "clear", absent reads "default". The command validates every key and value
// against the generated type before sending, so only a well-formed object
// reaches the wire. The json tags are the GraphQL field names, which is what
// the coverage matrix reads to count the fields as reached.
type CreateAgentTasksItemInput struct {
	// BlockedByPositions are the 1-based positions of other tasks in the same
	// call that block this one; omitted when the item names none.
	BlockedByPositions []int           `json:"blockedByPositions,omitempty"`
	Task               json.RawMessage `json:"task"`
}
