//go:build windows

package player

// ipcAddress is where mpv listens for commands: a named pipe.
func ipcAddress(name string) string {
	return `\\.\pipe\kumo-` + name
}
