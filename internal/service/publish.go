package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
)

// Pushing a bot's files to GitHub. The files are read from the workspace
// and sent through the Git Data API with the acting user's own token (never
// the token of whoever linked the repository): pushing is write access to
// their GitHub account. Nothing from the workspace is executed.

// RemotePushes reads a remote server's files for a push through its node's
// authenticated connection: the node selects the files with the shared
// filter and secret-file rules, then each selected file is read on its own.
// Nothing is written to the panel's disk.
type RemotePushes interface {
	Online(nodeID string) bool
	PushSet(ctx context.Context, nodeID, botID string, lim filesystem.PushLimits) (filesystem.PushSet, error)
	ReadFile(ctx context.Context, nodeID, botID, path string, max int64) ([]byte, error)
}

// pushSource is where a push reads a server's files: the panel's own
// workspace, or the node that runs the server.
type pushSource interface {
	set(ctx context.Context) (filesystem.PushSet, error)
	read(ctx context.Context, p string) ([]byte, error)
	close()
}

type localPush struct {
	w   *filesystem.Workspace
	lim filesystem.PushLimits
}

func (l localPush) set(context.Context) (filesystem.PushSet, error) { return l.w.PushSet(l.lim) }
func (l localPush) read(_ context.Context, p string) ([]byte, error) {
	return l.w.Read(p, l.lim.MaxFile)
}
func (l localPush) close() { l.w.Close() }

type remotePush struct {
	r           RemotePushes
	node, botID string
	lim         filesystem.PushLimits
}

func (r remotePush) set(ctx context.Context) (filesystem.PushSet, error) {
	return r.r.PushSet(ctx, r.node, r.botID, r.lim)
}
func (r remotePush) read(ctx context.Context, p string) ([]byte, error) {
	return r.r.ReadFile(ctx, r.node, r.botID, p, r.lim.MaxFile)
}
func (remotePush) close() {}

func (d *DeployService) pushLimits() filesystem.PushLimits {
	if d.PushLimits.MaxFiles > 0 && d.PushLimits.MaxBytes > 0 && d.PushLimits.MaxFile > 0 {
		return d.PushLimits
	}
	return filesystem.DefaultPushLimits
}

// openPush opens the files of b for a push. A server on a remote node is
// read from that node only, and refused while its agent is offline.
func (d *DeployService) openPush(b domain.Bot) (pushSource, error) {
	lim := d.pushLimits()
	if d.Bots.remote(b.NodeID) {
		if d.RemotePush == nil {
			return nil, domain.Invalid("publishing to GitHub is not available for servers on remote nodes on this panel")
		}
		if !d.RemotePush.Online(b.NodeID) {
			return nil, domain.Invalid("the server's node is offline; try again when its agent reconnects")
		}
		return remotePush{r: d.RemotePush, node: b.NodeID, botID: b.ID, lim: lim}, nil
	}
	w, err := d.Files.Open(b.ID)
	if err != nil {
		return nil, err
	}
	return localPush{w: w, lim: lim}, nil
}

// pushSet selects the files to push, turning limit violations into
// messages users can act on.
func pushSet(ctx context.Context, src pushSource) (filesystem.PushSet, error) {
	set, err := src.set(ctx)
	var ae *filesystem.ErrArchive
	if errors.As(err, &ae) {
		return filesystem.PushSet{}, domain.Invalid(ae.Msg)
	}
	return set, err
}

// PushPlan previews what a push would send.
func (d *DeployService) PushPlan(ctx context.Context, actor domain.User, botID string) (filesystem.PushSet, error) {
	b, err := d.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles)
	if err != nil {
		return filesystem.PushSet{}, err
	}
	src, err := d.openPush(b)
	if err != nil {
		return filesystem.PushSet{}, err
	}
	defer src.close()
	return pushSet(ctx, src)
}

// Owners lists where the actor may create repositories.
func (d *DeployService) Owners(ctx context.Context, actor domain.User) ([]github.Owner, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	tok, err := d.OAuth.GitHubPushToken(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	o, err := d.GH.Owners(ctx, tok)
	return o, ghError(err)
}

// PublishInput describes a new repository for a bot's files.
type PublishInput struct {
	Owner       string // "" or the user's login: their account; otherwise an organization
	Name        string
	Description string
	Private     bool
	AutoDeploy  bool // link with a push webhook afterwards
}

// Publish creates a GitHub repository, then (in the background, recorded as
// a "publish" operation) pushes the bot's files to it and links the bot to
// it, so later deployments come from the repository.
func (d *DeployService) Publish(ctx context.Context, actor domain.User, botID string, in PublishInput) (github.Repo, error) {
	if err := d.ready(); err != nil {
		return github.Repo{}, err
	}
	b, err := d.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin)
	if err != nil {
		return github.Repo{}, err
	}
	if cur, err := d.store().GetGitHubRepo(ctx, botID); err == nil {
		return github.Repo{}, domain.Invalid("this bot is already linked to " + cur.FullName + "; push to it instead")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return github.Repo{}, err
	}
	name := strings.TrimSpace(in.Name)
	if !github.ValidRepoName(name) {
		return github.Repo{}, domain.Invalid("repository names use letters, digits, '.', '-' and '_' (up to 100)")
	}
	if in.AutoDeploy && d.publicURL() == "" {
		return github.Repo{}, domain.Invalid("auto-deploy needs RIVET_PUBLIC_URL to be configured so GitHub can reach the panel")
	}
	tok, err := d.OAuth.GitHubPushToken(ctx, actor.ID)
	if err != nil {
		return github.Repo{}, err
	}
	// Refuse before creating anything if the files cannot be pushed.
	set, err := d.PushPlan(ctx, actor, botID)
	if err != nil {
		return github.Repo{}, err
	}
	if len(set.Files) == 0 {
		return github.Repo{}, domain.Invalid("the bot has no files to publish")
	}
	org := strings.TrimSpace(in.Owner)
	if org != "" {
		login, err := d.GH.Viewer(ctx, tok)
		if err != nil {
			return github.Repo{}, ghError(err)
		}
		if strings.EqualFold(org, login) {
			org = ""
		}
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		desc = b.Name + " (published from RivetPanel)"
	}
	repo, err := d.GH.CreateRepo(ctx, tok, org, name, desc, in.Private)
	if errors.Is(err, github.ErrConflict) {
		return github.Repo{}, domain.Invalid("a repository with that name already exists there")
	}
	if err != nil {
		return github.Repo{}, ghError(err)
	}
	d.startPush(botID, pushJob{actor: actor, full: repo.FullName, branch: repo.DefaultBranch, token: tok,
		message: "Initial commit from RivetPanel", publish: &PublishInput{AutoDeploy: in.AutoDeploy}})
	return repo, nil
}

// Push commits the bot's current files to its linked repository and branch
// (inside the linked root directory, leaving the rest of the repository
// alone). The branch must not move meanwhile: nothing is ever force-pushed.
func (d *DeployService) Push(ctx context.Context, actor domain.User, botID, message string) error {
	if err := d.ready(); err != nil {
		return err
	}
	b, err := d.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles)
	if err != nil {
		return err
	}
	link, err := d.store().GetGitHubRepo(ctx, botID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Invalid("link a repository or publish the bot to a new one first")
	}
	if err != nil {
		return err
	}
	tok, err := d.OAuth.GitHubPushToken(ctx, actor.ID)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Update from RivetPanel"
	}
	if len(message) > 2000 {
		return domain.Invalid("the commit message is too long")
	}
	if what, busy := d.Bots.Coord.Active(botID); busy {
		return domain.Invalid(what + " is in progress; push when it has finished")
	}
	// Refuse now (not in the background) when the files cannot be read, for
	// example because the server's node is offline.
	src, err := d.openPush(b)
	if err != nil {
		return err
	}
	src.close()
	d.startPush(botID, pushJob{actor: actor, full: link.FullName, branch: link.Branch, root: link.RootDir, token: tok, message: message})
	return nil
}

type pushJob struct {
	actor   domain.User
	full    string
	branch  string
	root    string
	token   string
	message string
	publish *PublishInput // set for a new repository: link the bot afterwards
}

func (d *DeployService) startPush(botID string, j pushJob) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if d.sem == nil {
			d.sem = make(chan struct{}, 2)
		}
		d.sem <- struct{}{}
		defer func() { <-d.sem }()
		d.runPush(botID, j)
	}()
}

func (d *DeployService) runPush(botID string, j pushJob) {
	base := d.ctx()
	ctx, cancel := context.WithTimeout(base, 20*time.Minute)
	defer cancel()
	label := j.full + "@" + j.branch
	op := d.Ops.Begin(ctx, OpStart{BotID: botID, Kind: domain.OpPublish, Trigger: "manual", ActorID: &j.actor.ID, SourceLabel: &label})
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(base), 10*time.Second)
	defer fcancel()
	fail := func(err error) {
		msg := deployMessage(err)
		if msg == "the deployment failed; see the panel logs" {
			msg = "the push failed; see the panel logs"
		}
		var ve *domain.ValidationError
		if !errors.As(err, &ve) && !errors.Is(err, context.DeadlineExceeded) {
			d.warn("push failed", err)
		}
		d.Ops.Finish(fctx, op, domain.OpFailed, "publish_failed", msg, nil)
	}
	claim, cctx, err := d.Bots.Coord.Claim(ctx, botID, "A push to GitHub", false)
	if err != nil {
		d.Ops.Finish(fctx, op, domain.OpFailed, "busy", err.Error(), nil)
		return
	}
	defer claim.Release()
	res, err := d.pushOnce(cctx, botID, j, op)
	if err != nil {
		fail(err)
		return
	}
	if j.publish != nil {
		d.Ops.Stage(fctx, op, "Linking the repository")
		if _, err := d.configure(cctx, j.actor, botID, ConfigureInput{FullName: j.full, Branch: j.branch, AutoDeploy: j.publish.AutoDeploy}); err != nil {
			fail(domain.Invalid("the files were pushed, but linking the repository failed: " + deployMessage(err)))
			return
		}
	}
	// The workspace is exactly this commit: record it as deployed.
	if res.commit != "" {
		_ = d.store().RecordDeploy(fctx, botID, &res.commit, nil, d.now().UnixMilli())
	}
	msg := res.summary(j.full, j.branch)
	d.Ops.Finish(fctx, op, domain.OpSucceeded, "", msg, map[string]any{"sha": res.commit, "files": res.files,
		"uploaded": res.uploaded, "url": "https://github.com/" + j.full})
}

type pushResult struct {
	commit   string // "" when GitHub already had exactly these files
	files    int
	uploaded int
}

func (r pushResult) summary(full, branch string) string {
	if r.commit == "" {
		return fmt.Sprintf("%s@%s already has these %d files; nothing to push", full, branch, r.files)
	}
	return fmt.Sprintf("Pushed %d files (%d changed) to %s@%s as %s", r.files, r.uploaded, full, branch, r.commit[:7])
}

func (d *DeployService) pushOnce(ctx context.Context, botID string, j pushJob, op string) (pushResult, error) {
	d.Ops.Stage(ctx, op, "Reading the branch")
	// A repository created a moment ago may need a few seconds before its
	// first commit is visible.
	var head string
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		head, err = d.GH.BranchHead(ctx, j.token, j.full, j.branch)
		if !errors.Is(err, github.ErrNotFound) || j.publish == nil {
			break
		}
		select {
		case <-ctx.Done():
			return pushResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	create := false
	switch {
	case errors.Is(err, github.ErrNotFound) && j.publish == nil:
		create = true // a new branch in an existing repository
	case err != nil:
		return pushResult{}, ghError(err)
	}
	existing := map[string]github.TreeEntry{}
	var headTree string
	if head != "" {
		tree, files, err := d.GH.CommitFiles(ctx, j.token, j.full, head)
		if err != nil {
			return pushResult{}, ghError(err)
		}
		headTree = tree
		for _, f := range files {
			existing[f.Path] = f
		}
	}

	d.Ops.Stage(ctx, op, "Collecting files")
	b, err := d.store().GetBot(ctx, botID)
	if err != nil {
		return pushResult{}, err
	}
	src, err := d.openPush(b)
	if err != nil {
		return pushResult{}, err
	}
	defer src.close()
	set, err := pushSet(ctx, src)
	if err != nil {
		return pushResult{}, err
	}
	if len(set.Files) == 0 {
		return pushResult{}, domain.Invalid("the bot has no files to push")
	}

	// Files outside the linked root directory stay as they are.
	prefix := ""
	if j.root != "" {
		prefix = j.root + "/"
	}
	var entries []github.TreeEntry
	if prefix != "" {
		for p, e := range existing {
			if !strings.HasPrefix(p, prefix) {
				entries = append(entries, e)
			}
		}
	}

	d.Ops.Stage(ctx, op, fmt.Sprintf("Uploading changed files (%d)", len(set.Files)))
	type item struct {
		i     int
		entry github.TreeEntry
		err   error
		fresh bool
	}
	jobs := make(chan int)
	results := make(chan item)
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				f := set.Files[i]
				it := item{i: i}
				var content []byte
				mode := "100644"
				if f.Link != "" {
					content, mode = []byte(f.Link), "120000"
				} else {
					content, it.err = src.read(ctx, f.Path)
					if f.Exec {
						mode = "100755"
					}
				}
				if it.err == nil {
					repoPath := path.Join(j.root, f.Path)
					sha := github.BlobSHA(content)
					if old, ok := existing[repoPath]; !ok || old.SHA != sha {
						if sha, it.err = d.GH.CreateBlob(ctx, j.token, j.full, content); it.err == nil {
							it.fresh = true
						}
					}
					it.entry = github.TreeEntry{Path: repoPath, Mode: mode, Type: "blob", SHA: sha}
				}
				results <- it
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range set.Files {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	res := pushResult{files: len(set.Files)}
	var firstErr error
	got := make([]github.TreeEntry, len(set.Files))
	for it := range results {
		if it.err != nil && firstErr == nil {
			firstErr = it.err
		}
		got[it.i] = it.entry
		if it.fresh {
			res.uploaded++
		}
	}
	if firstErr != nil {
		return pushResult{}, ghError(firstErr)
	}
	if err := ctx.Err(); err != nil {
		return pushResult{}, err
	}
	entries = append(entries, got...)

	d.Ops.Stage(ctx, op, "Creating the commit")
	tree, err := d.GH.CreateTree(ctx, j.token, j.full, entries)
	if err != nil {
		return pushResult{}, ghError(err)
	}
	if tree == headTree {
		return res, nil // identical content: no empty commit
	}
	var parents []string
	if head != "" {
		parents = []string{head}
	}
	commit, err := d.GH.CreateCommit(ctx, j.token, j.full, j.message, tree, parents)
	if err != nil {
		return pushResult{}, ghError(err)
	}
	if err := d.GH.MoveBranch(ctx, j.token, j.full, j.branch, commit, create); err != nil {
		if errors.Is(err, github.ErrConflict) {
			return pushResult{}, domain.Invalid("the branch changed on GitHub while pushing; deploy or pull those changes first, then push again")
		}
		return pushResult{}, ghError(err)
	}
	res.commit = commit
	return res, nil
}
