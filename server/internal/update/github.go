package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

// github calls GitHub's REST API for one repository.
type github struct {
	api       string // https://api.github.com
	repo      string // owner/name
	repoID    string // its number, which stays when it's renamed (or "")
	client    *http.Client
	token     string // "" for anonymous requests
	userAgent string
}

// root is the repository's address in the API: by its number when known,
// as the name of a renamed one may be taken by someone else.
func (g *github) root() string {
	if g.repoID != "" {
		return "/repositories/" + g.repoID
	}
	return "/repos/" + g.repo
}

// apiError is an error answer from GitHub.
type apiError struct {
	status      int
	message     string
	rateLimited bool
}

func (e *apiError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("GitHub answered %q (HTTP %d)", e.message, e.status)
	}
	return fmt.Sprintf("GitHub answered HTTP %d", e.status)
}

// request sends a GET to the API and returns the response when it's a
// success. Redirects (downloads go to other hosts) are followed; Go doesn't
// send the token along to another host.
func (g *github) request(ctx context.Context, path, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", g.userAgent)
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, cleanError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
		limited := resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"
		return nil, &apiError{status: resp.StatusCode, message: body.Message, rateLimited: limited}
	}
	return resp, nil
}

// getJSON decodes an API answer into out and returns its headers.
func (g *github) getJSON(ctx context.Context, path string, out any) (http.Header, error) {
	resp, err := g.request(ctx, path, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Comparisons and commits carry their diffs: big, but not that big.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out); err != nil {
		return nil, fmt.Errorf("reading GitHub's answer: %w", cleanError(err))
	}
	return resp.Header, nil
}

// cleanError keeps the query out of a failed request's address: after a
// redirect it can hold a download token.
func cleanError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	u, perr := url.Parse(ue.URL)
	if perr != nil {
		return ue.Err
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return &url.Error{Op: ue.Op, URL: u.String(), Err: ue.Err}
}

// escapeRef puts a branch or tag name in a URL path; its slashes stay.
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

type commitInfo struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message   string `json:"message"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// commit looks up a branch's (or tag's) commit.
func (g *github) commit(ctx context.Context, ref string) (*commitInfo, error) {
	var c commitInfo
	if _, err := g.getJSON(ctx, g.root()+"/commits/"+escapeRef(ref), &c); err != nil {
		return nil, err
	}
	if c.SHA == "" {
		return nil, fmt.Errorf("GitHub has no commit for %s", ref)
	}
	return &c, nil
}

// commitCount is how many commits lead to sha (it included): with one
// commit per page, the number of the last page. 0 when unknown.
func (g *github) commitCount(ctx context.Context, sha string) int {
	var commits []json.RawMessage
	h, err := g.getJSON(ctx, g.root()+"/commits?sha="+url.QueryEscape(sha)+"&per_page=1", &commits)
	if err != nil {
		return 0
	}
	if n := lastPage(h.Get("Link")); n > 0 {
		return n
	}
	return len(commits) // a single page
}

// lastPage reads the page number of rel="last" in a Link header.
func lastPage(link string) int {
	for _, part := range strings.Split(link, ",") {
		target, params, ok := strings.Cut(part, ";")
		if !ok || !strings.Contains(params, `rel="last"`) {
			continue
		}
		u, err := url.Parse(strings.Trim(strings.TrimSpace(target), "<>"))
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(u.Query().Get("page"))
		return n
	}
	return 0
}

// recentSubjects lists the subjects of the newest commits up to sha.
func (g *github) recentSubjects(ctx context.Context, sha string) ([]string, error) {
	var commits []commitInfo
	if _, err := g.getJSON(ctx, fmt.Sprintf("%s/commits?sha=%s&per_page=%d", g.root(), url.QueryEscape(sha), maxChanges), &commits); err != nil {
		return nil, err
	}
	out := []string{}
	for _, c := range commits {
		out = append(out, subject(c.Commit.Message))
	}
	return out, nil
}

// comparison is GitHub's comparison of two commits.
type comparison struct {
	Status   string       `json:"status"` // ahead, identical, behind or diverged
	AheadBy  int          `json:"ahead_by"`
	BehindBy int          `json:"behind_by"`
	Commits  []commitInfo `json:"commits"` // oldest first
}

func (g *github) compare(ctx context.Context, base, head string) (*comparison, error) {
	var cmp comparison
	if _, err := g.getJSON(ctx, g.root()+"/compare/"+escapeRef(base)+"..."+escapeRef(head), &cmp); err != nil {
		return nil, err
	}
	return &cmp, nil
}

// changes lists the subject lines of the commits, newest first, at most
// maxChanges.
func (cmp *comparison) changes() []string {
	out := []string{}
	for i := len(cmp.Commits) - 1; i >= 0 && len(out) < maxChanges; i-- {
		out = append(out, subject(cmp.Commits[i].Commit.Message))
	}
	return out
}

func subject(message string) string {
	s, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	return strings.TrimSpace(s)
}

// release is a GitHub Release.
type release struct {
	TagName         string `json:"tag_name"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
	HTMLURL         string `json:"html_url"`
	TargetCommitish string `json:"target_commitish"`
	PublishedAt     string `json:"published_at"`
	Assets          []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// releaseVersion is the version in a release's tag, after the system's
// prefix (windows-v, macos-v).
var releaseVersion = regexp.MustCompile(`^\d+(\.\d+)*$`)

// ---------------------------------------------------------------------------
// Token

// githubToken finds the user's GitHub token without ever asking for one:
// from the environment, then the GitHub CLI, then git's credential store.
// "" when there's none; requests are then anonymous, which works once the
// repository is public. The token is never logged, stored or shown.
func githubToken(ctx context.Context) string {
	for _, name := range []string{"KUMO_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if tok := strings.TrimSpace(os.Getenv(name)); tok != "" {
			return tok
		}
	}
	if tok := ghToken(ctx); tok != "" {
		return tok
	}
	return gitCredential(ctx)
}

// ghToken asks the GitHub CLI (gh auth login) for its token.
func ghToken(ctx context.Context) string {
	gh, ok := util.LookPath("gh")
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "token", "--hostname", "github.com")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	tok, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(tok)
}

// gitCredential asks git's credential helpers (Git Credential Manager, the
// keyring, a credentials file) for github.com. Nothing may prompt: no
// terminal prompt, no askpass program, no Credential Manager window.
func gitCredential(ctx context.Context) string {
	git, ok := util.LookPath("git")
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if tok, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "password="); ok && tok != "" {
			return tok
		}
	}
	return ""
}
