package api

import (
	"errors"
	"net/http"
	"runtime"

	"github.com/simo1337s/animetest/server/internal/winsetup"
)

// installPrograms opens the Windows setup that installs the programs Kumo
// uses (Scoop, Git, ani-cli, ffmpeg, mpv, yt-dlp...) in a PowerShell window
// on this computer.
func (s *Server) installPrograms(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("programs can only be installed from this computer")
	}
	if runtime.GOOS != "windows" {
		return nil, badRequest("Kumo installs its programs itself on Windows only: install them with your package manager")
	}
	if err := winsetup.Start(); err != nil {
		if errors.Is(err, winsetup.ErrRunning) {
			return nil, &apiError{http.StatusConflict, err.Error()}
		}
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}
