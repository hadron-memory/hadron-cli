package hostpoller

import "context"

// recovery is one reset this host collected before a restart, with the
// state the server holds for it now.
type recovery struct {
	Record ResetRecord
	Reset  ContextReset
}

// pendingRecoveries reads, for every reset this host recorded, its current
// state from the server (#3273): the phase comes from there, never from
// local memory (hadron-cli#1043, step 5). Only reads are made, never a
// protocol step. A reset the server no longer shows this host
// (CONTEXT_RESET_NOT_FOUND, HOST_OPERATION_NOT_COLLECTED) is dropped
// locally; any other read error keeps the record for the next pass.
//
// What each phase then calls for is decided separately (comment
// 6593190456, awaiting the gate's confirmation); nothing here acts on it.
func pendingRecoveries(ctx context.Context, s *State, rs ResetServer) []recovery {
	out := []recovery{}
	for _, rec := range s.inFlightResets() {
		r, err := rs.ReadReset(ctx, rec.ResetID)
		switch code := ResetStepCode(err); {
		case err == nil:
			out = append(out, recovery{Record: rec, Reset: r})
		case code == "CONTEXT_RESET_NOT_FOUND" || code == "HOST_OPERATION_NOT_COLLECTED":
			_ = s.dropReset(rec.ResetID)
		}
	}
	return out
}
