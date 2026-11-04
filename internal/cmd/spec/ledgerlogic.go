package spec

// Stub: the decision-ledger logic lands in the next commit (cli#662).

// ledgerDoc is one searchable text of a spec: its body or abstract.
type ledgerDoc struct {
	Citation string
	Field    string // "content" | "abstract"
	Text     string
}

// ledgerFinding is one line of the ledger report.
type ledgerFinding struct {
	Category  int      `json:"category"`
	Rule      string   `json:"rule"`
	Severity  string   `json:"severity"`
	ID        string   `json:"id,omitempty"`
	Citation  string   `json:"citation,omitempty"`
	Locations []string `json:"locations,omitempty"`
	Message   string   `json:"message"`
}

func checkLedger(registry string, docs []ledgerDoc, specs map[string]bool) []ledgerFinding {
	return nil
}
