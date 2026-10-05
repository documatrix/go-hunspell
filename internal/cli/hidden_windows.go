package cli

import (
	"os"
	"syscall"
)

// isHidden reports whether the file has the hidden attribute
// (FILE_ATTRIBUTE_HIDDEN), which FindFirstFile reports.
func isHidden(path string) bool {
	st, err := os.Lstat(path)
	if err != nil {
		return false
	}
	a, ok := st.Sys().(*syscall.Win32FileAttributeData)
	return ok && a.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
