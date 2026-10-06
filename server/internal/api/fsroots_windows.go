//go:build windows

package api

import "golang.org/x/sys/windows"

// fsRoots lists the drives (C:\, D:\…) for the folder picker to switch
// between.
func fsRoots() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) != 0 {
			out = append(out, string(rune('A'+i))+`:\`)
		}
	}
	return out
}
