package update

// writable is for macOS (see apply_mac.go): the Windows installer checks
// for itself.
func writable(string) bool { return true }
