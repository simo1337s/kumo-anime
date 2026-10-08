package api

import (
	"errors"
	"net/http"
	"runtime"

	"github.com/simo1337s/animetest/server/internal/macsetup"
	"github.com/simo1337s/animetest/server/internal/winsetup"
)

// installPrograms opens the setup that installs the programs Kumo uses on
// this computer: on Windows with Scoop (Git, ani-cli, ffmpeg, mpv, yt-dlp...)
// in a PowerShell window, on macOS by downloads into Kumo's folder, in
// Terminal.
func (s *Server) installPrograms(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("programs can only be installed from this computer")
	}
	var err error
	switch runtime.GOOS {
	case "windows":
		err = winsetup.Start()
	case "darwin":
		err = macsetup.Start()
	default:
		return nil, badRequest("Kumo installs its programs itself on Windows and macOS only: install them with your package manager")
	}
	if err != nil {
		if errors.Is(err, winsetup.ErrRunning) || errors.Is(err, macsetup.ErrRunning) {
			return nil, &apiError{http.StatusConflict, err.Error()}
		}
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}

// setupRunning reports whether the setup's window is open.
func setupRunning() bool {
	switch runtime.GOOS {
	case "windows":
		return winsetup.Running()
	case "darwin":
		return macsetup.Running()
	}
	return false
}
