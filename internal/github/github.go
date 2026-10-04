// Package github is a small GitHub REST client for the deployment feature:
// list repositories and branches, resolve a branch to a commit, download a
// tarball, and manage a push webhook. Downloading a tarball (instead of
// running git on the host) means no repository code, hook or filter ever runs
// on the host.
package github

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// FullNameRe validates "owner/repo".
	FullNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	branchRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,255}$`)
	shaRe      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ValidFullName reports whether s is "owner/name" with neither part being
// "." or ".." (which would change the API path).
func ValidFullName(s string) bool {
	if !FullNameRe.MatchString(s) {
		return false
	}
	o, n, _ := strings.Cut(s, "/")
	return strings.Trim(o, ".") != "" && strings.Trim(n, ".") != ""
}

// ValidBranch reports whether name is a plausible, safe branch name.
func ValidBranch(name string) bool {
	return branchRe.MatchString(name) && !strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "/") &&
		!strings.Contains(name, "..") && !strings.HasSuffix(name, "/") && !strings.HasSuffix(name, ".lock")
}

// ErrInvalid marks a malformed repository, branch or commit name.
var ErrInvalid = errors.New("invalid repository, branch or commit name")

// ErrNotFound means the repository or ref does not exist or is not visible to the token.
var ErrNotFound = errors.New("repository or branch not found (or no access)")

// ErrRateLimited means GitHub's API rate limit is used up (anonymous
// requests get 60 per hour per address).
var ErrRateLimited = errors.New("GitHub's rate limit is used up; try again later or connect your GitHub account for a higher limit")

// ErrUnauthorized means the token was rejected.
var ErrUnauthorized = errors.New("GitHub rejected the token; reconnect your GitHub account")

// Client talks to the GitHub API. API is overridable for tests and GitHub Enterprise.
type Client struct {
	API  string
	HTTP *http.Client
}

// Repo is a repository summary. The descriptive fields are filled by GetRepo
// (and by listings, where GitHub includes them).
type Repo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	Description   string `json:"description,omitempty"`
	Language      string `json:"language,omitempty"`
	Stars         int    `json:"stargazers_count,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
	PushedAt      string `json:"pushed_at,omitempty"`
	SizeKB        int64  `json:"size,omitempty"`
	License       *struct {
		SPDX string `json:"spdx_id"`
	} `json:"license,omitempty"`
}

// RepoRef is a repository reference pasted by a person.
type RepoRef struct {
	FullName string
	Branch   string // from a /tree/<branch>/... address; may contain "/" ambiguity, see ParseRepoRef
	Path     string // folder inside the repository
}

// ParseRepoRef accepts "owner/name", "github.com/owner/name",
// "https://github.com/owner/name(.git)", ".../tree/<branch>[/<folder>]" and
// "git@github.com:owner/name.git". A branch in a /tree/ address is taken as
// the first path segment (branch names containing "/" need the branch picker).
func ParseRepoRef(in string) (RepoRef, error) {
	s := strings.TrimSpace(in)
	s = strings.TrimPrefix(s, "git+")
	if rest, ok := strings.CutPrefix(s, "git@github.com:"); ok {
		s = "github.com/" + rest
	}
	for _, p := range []string{"https://", "http://", "ssh://git@", "git://"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimPrefix(s, "github.com/")
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return RepoRef{}, ErrInvalid
	}
	ref := RepoRef{FullName: parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")}
	if !ValidFullName(ref.FullName) {
		return RepoRef{}, ErrInvalid
	}
	if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
		ref.Branch = parts[3]
		if !ValidBranch(ref.Branch) {
			return RepoRef{}, ErrInvalid
		}
		if parts[2] == "tree" && len(parts) > 4 {
			ref.Path = strings.Join(parts[4:], "/")
		}
	}
	return ref, nil
}

func (c *Client) base() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return "https://api.github.com"
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) do(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "rivetpanel")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc().Do(req)
}

func statusErr(res *http.Response) error {
	if (res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests) &&
		res.Header.Get("X-RateLimit-Remaining") == "0" {
		return ErrRateLimited
	}
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound, http.StatusForbidden:
		return ErrNotFound // GitHub hides private repositories behind 404
	}
	return fmt.Errorf("github: unexpected status %d", res.StatusCode)
}

func (c *Client) getJSON(ctx context.Context, path, token string, out any) error {
	res, err := c.do(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return statusErr(res)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}

// Repos lists repositories the token can push to or read, most recently updated first (up to 300).
func (c *Client) Repos(ctx context.Context, token string) ([]Repo, error) {
	var all []Repo
	for page := 1; page <= 3; page++ {
		var pg []Repo
		q := fmt.Sprintf("/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member&page=%d", page)
		if err := c.getJSON(ctx, q, token, &pg); err != nil {
			return nil, err
		}
		all = append(all, pg...)
		if len(pg) < 100 {
			break
		}
	}
	return all, nil
}

// GetRepo returns one repository (token may be empty for public repositories).
func (c *Client) GetRepo(ctx context.Context, token, fullName string) (Repo, error) {
	if !ValidFullName(fullName) {
		return Repo{}, ErrInvalid
	}
	var r Repo
	err := c.getJSON(ctx, "/repos/"+fullName, token, &r)
	return r, err
}

// Branches lists branch names (up to 300).
func (c *Client) Branches(ctx context.Context, token, fullName string) ([]string, error) {
	if !ValidFullName(fullName) {
		return nil, ErrInvalid
	}
	var out []string
	for page := 1; page <= 3; page++ {
		var pg []struct{ Name string }
		if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/branches?per_page=100&page=%d", fullName, page), token, &pg); err != nil {
			return nil, err
		}
		for _, b := range pg {
			out = append(out, b.Name)
		}
		if len(pg) < 100 {
			break
		}
	}
	return out, nil
}

// BranchSHA resolves a branch to its head commit.
func (c *Client) BranchSHA(ctx context.Context, token, fullName, branch string) (string, error) {
	if !ValidFullName(fullName) || !ValidBranch(branch) {
		return "", ErrInvalid
	}
	var o struct{ SHA string }
	if err := c.getJSON(ctx, "/repos/"+fullName+"/commits/"+url.PathEscape(branch), token, &o); err != nil {
		return "", err
	}
	if !shaRe.MatchString(o.SHA) {
		return "", errors.New("github returned an invalid commit id")
	}
	return o.SHA, nil
}

// Tarball opens the gzip tarball of a commit. The API redirects to codeload;
// Go drops the Authorization header on the cross-host redirect.
func (c *Client) Tarball(ctx context.Context, token, fullName, sha string) (io.ReadCloser, error) {
	if !ValidFullName(fullName) || !shaRe.MatchString(sha) {
		return nil, ErrInvalid
	}
	hc := *c.hc()
	hc.Timeout = 0 // bounded by ctx; large repositories take longer than an API call
	cc := &Client{API: c.API, HTTP: &hc}
	res, err := cc.do(ctx, http.MethodGet, "/repos/"+fullName+"/tarball/"+sha, token, nil)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		return nil, statusErr(res)
	}
	return res.Body, nil
}

// CreateHook registers a push webhook and returns its id.
func (c *Client) CreateHook(ctx context.Context, token, fullName, hookURL, secret string) (int64, error) {
	if !ValidFullName(fullName) {
		return 0, ErrInvalid
	}
	res, err := c.do(ctx, http.MethodPost, "/repos/"+fullName+"/hooks", token, map[string]any{
		"name": "web", "active": true, "events": []string{"push"},
		"config": map[string]string{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"},
	})
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return 0, statusErr(res)
	}
	var o struct{ ID int64 }
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&o); err != nil {
		return 0, err
	}
	return o.ID, nil
}

// DeleteHook removes a webhook; a missing one is not an error.
func (c *Client) DeleteHook(ctx context.Context, token, fullName string, id int64) error {
	if !ValidFullName(fullName) {
		return ErrInvalid
	}
	res, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/hooks/%d", fullName, id), token, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotFound {
		return nil
	}
	return statusErr(res)
}

// ValidSHA reports whether s is a full commit id.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// Commit is one commit in a comparison.
type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"` // first line only
	Author  string `json:"author"`
}

// ChangedFile is one file in a comparison.
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | modified | removed | renamed | ...
}

// Comparison describes what changed between two commits (bounded).
type Comparison struct {
	HeadSHA      string        `json:"head_sha"`
	AheadBy      int           `json:"ahead_by"`
	BehindBy     int           `json:"behind_by"`
	Commits      []Commit      `json:"commits"`
	Files        []ChangedFile `json:"files"`
	FilesTrimmed bool          `json:"files_trimmed"`
}

// Compare lists commits and changed files from base to head. GitHub itself
// caps the file list (300); the result is further bounded here.
func (c *Client) Compare(ctx context.Context, token, fullName, base, head string) (Comparison, error) {
	if !ValidFullName(fullName) || !shaRe.MatchString(base) || !shaRe.MatchString(head) {
		return Comparison{}, ErrInvalid
	}
	var o struct {
		AheadBy  int `json:"ahead_by"`
		BehindBy int `json:"behind_by"`
		Commits  []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"commits"`
		Files []struct {
			Filename string `json:"filename"`
			Status   string `json:"status"`
		} `json:"files"`
	}
	if err := c.getJSON(ctx, "/repos/"+fullName+"/compare/"+base+"..."+head+"?per_page=50", token, &o); err != nil {
		return Comparison{}, err
	}
	out := Comparison{HeadSHA: head, AheadBy: o.AheadBy, BehindBy: o.BehindBy}
	for i := len(o.Commits) - 1; i >= 0 && len(out.Commits) < 20; i-- { // newest first
		cm := o.Commits[i]
		msg, _, _ := strings.Cut(cm.Commit.Message, "\n")
		out.Commits = append(out.Commits, Commit{SHA: cm.SHA, Message: trunc(msg, 200), Author: trunc(cm.Commit.Author.Name, 100)})
	}
	for _, f := range o.Files {
		if len(out.Files) >= 200 {
			out.FilesTrimmed = true
			break
		}
		out.Files = append(out.Files, ChangedFile{Path: trunc(f.Filename, 300), Status: f.Status})
	}
	return out, nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---- writing: repository creation and pushing through the Git Data API ----
//
// Pushing never runs git on the host: file contents become blobs, a tree
// lists them, a commit points at the tree and the branch ref moves to the
// commit. That needs a token with the repo scope.

// ErrConflict means the branch moved while a push was being prepared, or the
// repository name is taken.
var ErrConflict = errors.New("the repository changed or already exists")

// Owner is an account a repository can be created under.
type Owner struct {
	Login string `json:"login"`
	Org   bool   `json:"org"`
}

// Viewer returns the token owner's login.
func (c *Client) Viewer(ctx context.Context, token string) (string, error) {
	var o struct{ Login string }
	if err := c.getJSON(ctx, "/user", token, &o); err != nil {
		return "", err
	}
	return o.Login, nil
}

// Owners lists the user and the organizations they belong to (up to 100).
func (c *Client) Owners(ctx context.Context, token string) ([]Owner, error) {
	login, err := c.Viewer(ctx, token)
	if err != nil {
		return nil, err
	}
	out := []Owner{{Login: login}}
	var orgs []struct{ Login string }
	if err := c.getJSON(ctx, "/user/orgs?per_page=100", token, &orgs); err == nil {
		for _, o := range orgs {
			out = append(out, Owner{Login: o.Login, Org: true})
		}
	}
	return out, nil
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// ValidRepoName reports whether name is usable as a new repository name.
func ValidRepoName(name string) bool {
	return repoNameRe.MatchString(name) && name != "." && name != ".." && !strings.HasSuffix(strings.ToLower(name), ".git")
}

// CreateRepo creates a repository under the user (org "") or an
// organization. auto_init gives it a first commit, which the Git Data API
// needs (it cannot write to an empty repository).
func (c *Client) CreateRepo(ctx context.Context, token, org, name, description string, private bool) (Repo, error) {
	if !ValidRepoName(name) || (org != "" && !repoNameRe.MatchString(org)) {
		return Repo{}, ErrInvalid
	}
	p := "/user/repos"
	if org != "" {
		p = "/orgs/" + org + "/repos"
	}
	res, err := c.do(ctx, http.MethodPost, p, token, map[string]any{
		"name": name, "description": trunc(description, 350), "private": private, "auto_init": true,
	})
	if err != nil {
		return Repo{}, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusCreated:
	case http.StatusUnprocessableEntity:
		return Repo{}, ErrConflict
	default:
		return Repo{}, statusErr(res)
	}
	var r Repo
	return r, json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&r)
}

func (c *Client) postJSON(ctx context.Context, method, path, token string, body, out any, want int) error {
	res, err := c.do(ctx, method, path, token, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		if res.StatusCode == http.StatusUnprocessableEntity || res.StatusCode == http.StatusConflict {
			return ErrConflict
		}
		return statusErr(res)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}

// BranchHead returns the commit a branch points at; ErrNotFound when the
// branch does not exist.
func (c *Client) BranchHead(ctx context.Context, token, fullName, branch string) (string, error) {
	if !ValidFullName(fullName) || !ValidBranch(branch) {
		return "", ErrInvalid
	}
	var o struct {
		Object struct{ SHA string } `json:"object"`
	}
	if err := c.getJSON(ctx, "/repos/"+fullName+"/git/ref/heads/"+branch, token, &o); err != nil {
		return "", err
	}
	if !shaRe.MatchString(o.Object.SHA) {
		return "", errors.New("github returned an invalid commit id")
	}
	return o.Object.SHA, nil
}

// TreeEntry is one file of a git tree.
type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"` // 100644, 100755 or 120000
	Type string `json:"type"` // blob
	SHA  string `json:"sha"`
}

// CommitFiles returns a commit's tree id and every file in it. Truncated
// trees (very large repositories) are refused rather than pushed over
// incompletely.
func (c *Client) CommitFiles(ctx context.Context, token, fullName, commit string) (string, []TreeEntry, error) {
	if !ValidFullName(fullName) || !shaRe.MatchString(commit) {
		return "", nil, ErrInvalid
	}
	var cm struct {
		Tree struct{ SHA string } `json:"tree"`
	}
	if err := c.getJSON(ctx, "/repos/"+fullName+"/git/commits/"+commit, token, &cm); err != nil {
		return "", nil, err
	}
	var t struct {
		Tree      []TreeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := c.getJSON(ctx, "/repos/"+fullName+"/git/trees/"+cm.Tree.SHA+"?recursive=1", token, &t); err != nil {
		return "", nil, err
	}
	if t.Truncated {
		return "", nil, errors.New("the repository is too large to update through the GitHub API")
	}
	out := t.Tree[:0]
	for _, e := range t.Tree {
		if e.Type == "blob" {
			out = append(out, e)
		}
	}
	return cm.Tree.SHA, out, nil
}

// CreateBlob uploads file contents and returns the blob id.
func (c *Client) CreateBlob(ctx context.Context, token, fullName string, content []byte) (string, error) {
	var o struct{ SHA string }
	err := c.postJSON(ctx, http.MethodPost, "/repos/"+fullName+"/git/blobs", token,
		map[string]string{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64"}, &o, http.StatusCreated)
	return o.SHA, err
}

// CreateTree writes a complete tree (no base tree: files not listed are
// absent from it) and returns its id.
func (c *Client) CreateTree(ctx context.Context, token, fullName string, entries []TreeEntry) (string, error) {
	var o struct{ SHA string }
	err := c.postJSON(ctx, http.MethodPost, "/repos/"+fullName+"/git/trees", token, map[string]any{"tree": entries}, &o, http.StatusCreated)
	return o.SHA, err
}

// CreateCommit creates a commit and returns its id.
func (c *Client) CreateCommit(ctx context.Context, token, fullName, message, tree string, parents []string) (string, error) {
	var o struct{ SHA string }
	err := c.postJSON(ctx, http.MethodPost, "/repos/"+fullName+"/git/commits", token,
		map[string]any{"message": message, "tree": tree, "parents": parents}, &o, http.StatusCreated)
	return o.SHA, err
}

// MoveBranch fast-forwards a branch to commit (never forced: a branch that
// moved meanwhile returns ErrConflict), or creates it when create is set.
func (c *Client) MoveBranch(ctx context.Context, token, fullName, branch, commit string, create bool) error {
	if !ValidFullName(fullName) || !ValidBranch(branch) || !shaRe.MatchString(commit) {
		return ErrInvalid
	}
	if create {
		return c.postJSON(ctx, http.MethodPost, "/repos/"+fullName+"/git/refs", token,
			map[string]any{"ref": "refs/heads/" + branch, "sha": commit}, nil, http.StatusCreated)
	}
	return c.postJSON(ctx, http.MethodPatch, "/repos/"+fullName+"/git/refs/heads/"+branch, token,
		map[string]any{"sha": commit, "force": false}, nil, http.StatusOK)
}

// BlobSHA is git's object id for file contents, so unchanged files need no upload.
func BlobSHA(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}
