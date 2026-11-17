package delivery

// Windows has no Unix uid; retain the existing creation behavior.
func allowBindingCreation(string) error { return nil }
