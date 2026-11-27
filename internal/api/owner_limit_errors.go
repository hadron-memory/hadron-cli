package api

// OwnerLimitDetails is the curated reader for the two size and count refusals a
// gated door can answer (hadron-cli#3072): OWNER_LIMIT_REACHED (hadron-server#5009,
// #5015, #5017) and NODE_CONTENT_TOO_LARGE (#5019). It forwards only the keys those
// refusals define, each only when the server sent it with the expected JSON type,
// and nothing else from the extensions. A key the server omitted stays absent:
// never null, never filled in. The server leaves out `limit` and `ownerId` for a
// caller who cannot manage the owner, and the CLI never computes or guesses a
// number (cor:acl:150, the same-answer refusal).
//
// It returns nil for any other error, so a caller can run it after the other
// readers and let it win only for these codes.
func OwnerLimitDetails(err error) map[string]any {
	for _, e := range graphQLErrors(err) {
		if e == nil {
			continue
		}
		switch code := extensionCode(e); code {
		case "OWNER_LIMIT_REACHED":
			details := map[string]any{"code": code}
			for _, key := range []string{"limitKey", "limit", "ownerType", "ownerId"} {
				if v, ok := e.Extensions[key].(string); ok {
					details[key] = v
				}
			}
			if v, ok := e.Extensions["callerCanManageOwner"].(bool); ok {
				details["callerCanManageOwner"] = v
			}
			return details
		case "NODE_CONTENT_TOO_LARGE":
			details := map[string]any{"code": code}
			if v, ok := e.Extensions["maxBytes"].(float64); ok {
				details["maxBytes"] = v
			}
			return details
		}
	}
	return nil
}
