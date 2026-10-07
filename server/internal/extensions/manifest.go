// Package extensions runs marketplace extensions (torrent, online
// streaming and manga providers, plus a subset of the plugin API) inside a
// sandboxed goja JavaScript runtime, and manages installing them from the
// community marketplace.
package extensions

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	TypeTorrent      = "anime-torrent-provider"
	TypeOnlineStream = "onlinestream-provider"
	TypeManga        = "manga-provider"
	TypeCustomSource = "custom-source"
	TypePlugin       = "plugin"
)

type ConfigField struct {
	Type    string `json:"type"` // text | switch | select
	Name    string `json:"name"`
	Label   string `json:"label"`
	Options []struct {
		Value string `json:"value"`
		Label string `json:"label"`
	} `json:"options,omitempty"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

type UserConfig struct {
	Version        int           `json:"version"`
	RequiresConfig bool          `json:"requiresConfig"`
	Fields         []ConfigField `json:"fields"`
}

type SavedUserConfig struct {
	Version int               `json:"version"`
	Values  map[string]string `json:"values"`
}

type PluginPermissions struct {
	Scopes []string `json:"scopes"`
	Allow  struct {
		NetworkAccess struct {
			AllowedDomains []string `json:"allowedDomains"`
			Reasoning      string   `json:"reasoning"`
		} `json:"networkAccess"`
		UnsafeFlags []struct {
			Flag   string `json:"flag"`
			Reason string `json:"reason"`
		} `json:"unsafeFlags"`
		ReadPaths     []string `json:"readPaths"`
		WritePaths    []string `json:"writePaths"`
		CommandScopes []any    `json:"commandScopes"`
	} `json:"allow"`
}

type PluginManifest struct {
	Version     string            `json:"version"`
	Permissions PluginPermissions `json:"permissions"`
}

type Manifest struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Version          string          `json:"version"`
	SemverConstraint string          `json:"semverConstraint,omitempty"`
	ManifestURI      string          `json:"manifestURI"`
	Language         string          `json:"language"`
	Type             string          `json:"type"`
	Description      string          `json:"description"`
	Author           string          `json:"author"`
	Icon             string          `json:"icon"`
	Website          string          `json:"website"`
	Readme           string          `json:"readme,omitempty"`
	Notes            string          `json:"notes,omitempty"`
	Lang             string          `json:"lang"`
	UserConfig       *UserConfig     `json:"userConfig,omitempty"`
	Payload          string          `json:"payload,omitempty"`
	PayloadURI       string          `json:"payloadURI,omitempty"`
	Plugin           *PluginManifest `json:"plugin,omitempty"`
	IsDevelopment    bool            `json:"isDevelopment,omitempty"`
}

var reID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9\-_.]*[a-zA-Z0-9]$`)

// Validate performs the usual sanity checks on a manifest (a bit lenient
// on lengths, since marketplace entries sometimes exceed them).
func (m *Manifest) Validate() error {
	var missing []string
	if m.ID == "" {
		missing = append(missing, "id")
	}
	if m.Name == "" {
		missing = append(missing, "name")
	}
	if m.Version == "" {
		missing = append(missing, "version")
	}
	if m.Type == "" {
		missing = append(missing, "type")
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid manifest: missing %s", strings.Join(missing, ", "))
	}
	if !reID.MatchString(m.ID) || len(m.ID) > 64 {
		return fmt.Errorf("invalid extension id %q", m.ID)
	}
	switch m.Type {
	case TypeTorrent, TypeOnlineStream, TypeManga, TypeCustomSource, TypePlugin:
	default:
		return fmt.Errorf("unsupported extension type %q", m.Type)
	}
	switch strings.ToLower(m.Language) {
	case "javascript", "typescript", "":
	default:
		return fmt.Errorf("unsupported extension language %q", m.Language)
	}
	if m.Type == TypePlugin && (m.Plugin == nil || m.Plugin.Version != "1") {
		return errors.New("plugin manifest must declare plugin.version \"1\"")
	}
	if m.IsDevelopment {
		return errors.New("development extensions can't be installed remotely")
	}
	m.Lang = strings.ToLower(m.Lang)
	if m.Lang == "" {
		m.Lang = "en"
	} else if m.Lang == "all" {
		m.Lang = "multi"
	}
	return nil
}
