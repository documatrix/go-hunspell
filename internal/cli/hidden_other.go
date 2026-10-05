//go:build !windows

package cli

// isHidden reports whether the file has the hidden attribute of Windows,
// which other systems do not have.
func isHidden(string) bool { return false }
