package cmdutil

// ActionAnswerDTO is the server's register answer, shared by action receipts.
// Null locations stay null; WITHHELD never gains a client-derived mapping.
type ActionAnswerDTO struct {
	Code    string  `json:"code"`
	State   string  `json:"state"`
	TaskLoc *string `json:"taskLoc"`
	TaskURN *string `json:"taskUrn"`
}
