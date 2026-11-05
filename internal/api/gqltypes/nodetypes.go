package gqltypes

// NodeTypeList preserves omission versus an explicitly empty list. A nil
// pointer is omitted; a pointer to [] sends [], leaving validation to the
// server. genqlient's default []string with omitempty drops both (cli#1528).
type NodeTypeList = *[]string
