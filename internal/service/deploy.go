package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// DeployService deploys a bot's source from GitHub into its workspace, on
// demand and on push (webhook). Repository code is only ever downloaded as a
// tarball and unpacked with the contained extractor; nothing from a repository
// executes on the host.
type DeployService struct {
	Bots      *BotService
	OAuth     *OAuthService
	Files     *filesystem.Manager
	GH        *github.Client
	Keys      *secrets.Keyring
	Alerts    *AlertService // optional
	PublicURL string
	Limits    filesystem.BackupLimits
	Log       *slog.Logger
	Now       func() time.Time
	Ops       *Operations // optional: deployment history
	AI        *AIService  // optional: refines repository analysis
	// MinFreeDisk is the free space a deployment needs to start.
	MinFreeDisk int64
	// PollInterval is how often auto-deploy sources without a webhook (public
	// repositories of other people, or a webhook GitHub refused) are checked
	// for new commits. 0 = 5 minutes; negative disables polling.
	PollInterval time.Duration
	// Remote deploys to servers on rivet-agent nodes (nil: refused). The
	// tarball is streamed to the assigned node and never unpacked here.
	Remote RemoteDeploys
	// RemotePush reads remote servers' files for GitHub publish/push (nil:
	// refused for remote servers).
	RemotePush RemotePushes
	// PushLimits bound one publish/push (zero: filesystem.DefaultPushLimits).
	PushLimits filesystem.PushLimits

	baseCtx context.Context
	sem     chan struct{}
	mu      sync.Mutex
	jobs    map[string]*jobState
	seen    map[string]time.Time // recent webhook delivery ids
	polled  map[string]string    // bot id -> branch head already queued by polling
	wg      sync.WaitGroup
}

// RemoteDeploys is the node-routed, authenticated deployment surface. A
// deployment is staged on the node (fully validated, workspace untouched),
// applied only after the panel re-checks that it is still wanted, and kept
// reversible until the panel has recorded the commit. A transaction that is
// never completed is rolled back by the node.
type RemoteDeploys interface {
	Online(nodeID string) bool
	BeginDeploy(ctx context.Context, nodeID, botID string, src io.Reader, rootDir string, limits filesystem.BackupLimits) (transaction string, err error)
	ApplyDeploy(ctx context.Context, nodeID, botID, transaction string) (files int, err error)
	CompleteTransaction(ctx context.Context, nodeID, botID, transaction string, commit bool) error
}

// Start sets the context deploy jobs run under (they outlive HTTP requests).
func (d *DeployService) Start(ctx context.Context) {
	d.baseCtx = ctx
	d.sem = make(chan struct{}, 2)
	if d.PollInterval >= 0 {
		go d.pollLoop(ctx)
	}
}

// Wait blocks until running jobs finish.
func (d *DeployService) Wait() { d.wg.Wait() }

func (d *DeployService) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *DeployService) store() Store { return d.Bots.Store }

func (d *DeployService) ctx() context.Context {
	if d.baseCtx != nil {
		return d.baseCtx
	}
	return context.Background()
}

// WebhookURL is where GitHub delivers push events.
func (d *DeployService) WebhookURL() string { return d.publicURL() + "/api/v1/webhooks/github" }

// publicURL follows runtime changes made on the settings page.
func (d *DeployService) publicURL() string {
	if u := d.OAuth.CurrentPublicURL(); u != "" {
		return u
	}
	return d.PublicURL
}

// ready refuses GitHub work while GitHub sign-in is not configured.
func (d *DeployService) ready() error {
	if !d.OAuth.Enabled("github") {
		return domain.Invalid("GitHub sign-in is not configured on this panel; an administrator can set it up under Administration → Panel settings")
	}
	return nil
}

func secretNS(botID string) string { return secrets.GitHubNS(botID) }

// ---- listing ----

func (d *DeployService) token(ctx context.Context, userID string) (string, error) {
	t, err := d.OAuth.GitHubToken(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", domain.Invalid("connect your GitHub account in Settings first")
	}
	return t, err
}

// optToken returns the user's GitHub token, or "" when they have not
// connected GitHub (public repositories work without one, with GitHub's
// lower anonymous rate limit).
func (d *DeployService) optToken(ctx context.Context, userID string) (string, error) {
	t, err := d.OAuth.GitHubToken(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	return t, err
}

func ghError(err error) error {
	switch {
	case errors.Is(err, github.ErrNotFound), errors.Is(err, github.ErrUnauthorized), errors.Is(err, github.ErrInvalid),
		errors.Is(err, github.ErrConflict):
		return domain.Invalid(err.Error())
	case errors.Is(err, github.ErrRateLimited):
		return domain.Invalid(err.Error())
	}
	return err
}

// Repos lists the repositories visible to the user's GitHub token.
func (d *DeployService) Repos(ctx context.Context, actor domain.User) ([]github.Repo, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	tok, err := d.token(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	r, err := d.GH.Repos(ctx, tok)
	return r, ghError(err)
}

// Branches lists a repository's branches (public repositories work without
// a GitHub connection).
func (d *DeployService) Branches(ctx context.Context, actor domain.User, fullName string) ([]string, error) {
	tok, err := d.optToken(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	b, err := d.GH.Branches(ctx, tok, fullName)
	return b, ghError(err)
}

// RepoLookup is a repository a person pasted, resolved for the creation flow.
type RepoLookup struct {
	Repo     github.Repo `json:"repo"`
	Branch   string      `json:"branch"`   // from the pasted address, else the default branch
	RootDir  string      `json:"root_dir"` // from the pasted address
	Branches []string    `json:"branches"`
	// Connected reports whether the person's own GitHub token was used (and
	// would be used for deployments); otherwise the repository is read
	// anonymously and must stay public.
	Connected bool `json:"connected"`
}

// Lookup resolves any GitHub address or owner/name to a repository, with or
// without a GitHub connection.
func (d *DeployService) Lookup(ctx context.Context, actor domain.User, ref string) (RepoLookup, error) {
	r, err := github.ParseRepoRef(ref)
	if err != nil {
		return RepoLookup{}, domain.Invalid("enter a GitHub repository such as owner/name or https://github.com/owner/name")
	}
	tok, err := d.optToken(ctx, actor.ID)
	if err != nil {
		return RepoLookup{}, err
	}
	repo, err := d.GH.GetRepo(ctx, tok, r.FullName)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) && tok == "" {
			return RepoLookup{}, domain.Invalid("repository not found; private repositories need a GitHub connection (Settings → Connected accounts)")
		}
		return RepoLookup{}, ghError(err)
	}
	out := RepoLookup{Repo: repo, Branch: r.Branch, RootDir: r.Path, Connected: tok != ""}
	if out.Branch == "" {
		out.Branch = repo.DefaultBranch
	}
	if out.Branches, err = d.GH.Branches(ctx, tok, repo.FullName); err != nil {
		out.Branches = []string{out.Branch} // listing is a convenience
	}
	return out, nil
}

// ---- configuration ----

// ConfigureInput selects what to deploy.
type ConfigureInput struct {
	FullName   string
	Branch     string
	RootDir    string
	AutoDeploy bool
}

// RepoView is a bot's GitHub source as shown to users. Secret is set only in
// the response that created it, and only when GitHub could not be given the
// webhook automatically.
type RepoView struct {
	FullName       string
	Branch         string
	RootDir        string
	Private        bool
	AutoDeploy     bool
	HookCreated    bool
	WebhookURL     string
	Secret         string
	LastSHA        string
	LastDeployedMS int64
	LastError      string
	Deploying      bool
	// Polling: auto-deploy works by checking the branch periodically because
	// no webhook could be installed.
	Polling bool
	// PendingPushMS is when a push arrived that waits for the server's
	// offline node to reconnect (0 = none).
	PendingPushMS int64
}

func cleanRoot(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == "/" {
		return "", nil
	}
	if len(s) > 200 || strings.ContainsAny(s, "\\\x00") {
		return "", domain.Invalid("invalid root directory")
	}
	c := strings.Trim(path.Clean("/"+s), "/")
	if c == "" || !filepath.IsLocal(c) {
		return "", domain.Invalid("invalid root directory")
	}
	return c, nil
}

func (d *DeployService) view(r domain.GitHubRepo) RepoView {
	v := RepoView{FullName: r.FullName, Branch: r.Branch, RootDir: r.RootDir, Private: r.Private, AutoDeploy: r.AutoDeploy,
		HookCreated: r.HookID != nil, WebhookURL: d.WebhookURL(), Deploying: d.isRunning(r.BotID),
		Polling: r.AutoDeploy && r.HookID == nil && d.PollInterval >= 0}
	if r.LastSHA != nil {
		v.LastSHA = *r.LastSHA
	}
	if r.LastDeployedMS != nil {
		v.LastDeployedMS = *r.LastDeployedMS
	}
	if r.LastError != nil {
		v.LastError = *r.LastError
	}
	return v
}

func (d *DeployService) isRunning(botID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	j := d.jobs[botID]
	return j != nil && j.running
}

// Get returns a bot's GitHub source (ErrNotFound when none).
func (d *DeployService) Get(ctx context.Context, actor domain.User, botID string) (RepoView, error) {
	if _, err := d.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles); err != nil {
		return RepoView{}, err
	}
	r, err := d.store().GetGitHubRepo(ctx, botID)
	if err != nil {
		return RepoView{}, err
	}
	v := d.view(r)
	if p, err := d.store().GetPendingPush(ctx, botID); err == nil && p.Link == linkKey(r) {
		v.PendingPushMS = p.AtMS
	}
	return v, nil
}

// Configure links (or re-links) a bot to a repository, using the acting user's
// GitHub token, and creates or removes the push webhook according to AutoDeploy.
func (d *DeployService) Configure(ctx context.Context, actor domain.User, botID string, in ConfigureInput) (RepoView, error) {
	if _, err := d.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin); err != nil {
		return RepoView{}, err
	}
	return d.configure(ctx, actor, botID, in)
}

func (d *DeployService) configure(ctx context.Context, actor domain.User, botID string, in ConfigureInput) (RepoView, error) {
	if !github.ValidFullName(in.FullName) {
		return RepoView{}, domain.Invalid("repository must look like owner/name")
	}
	if !github.ValidBranch(in.Branch) {
		return RepoView{}, domain.Invalid("invalid branch name")
	}
	root, err := cleanRoot(in.RootDir)
	if err != nil {
		return RepoView{}, err
	}
	// Without a public address (or a GitHub token) GitHub cannot deliver
	// webhooks: auto-deploy then polls the branch instead.
	if in.AutoDeploy && d.publicURL() == "" && d.PollInterval < 0 {
		return RepoView{}, domain.Invalid("auto-deploy needs RIVET_PUBLIC_URL to be configured so GitHub can reach the panel")
	}
	tok, err := d.optToken(ctx, actor.ID)
	if err != nil {
		return RepoView{}, err
	}
	repo, err := d.GH.GetRepo(ctx, tok, in.FullName)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) && tok == "" {
			return RepoView{}, domain.Invalid("repository not found; private repositories need a GitHub connection (Settings → Connected accounts)")
		}
		return RepoView{}, ghError(err)
	}
	if _, err := d.GH.BranchSHA(ctx, tok, in.FullName, in.Branch); err != nil {
		return RepoView{}, ghError(err)
	}

	now := d.now().UnixMilli()
	cur, err := d.store().GetGitHubRepo(ctx, botID)
	exists := err == nil
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return RepoView{}, err
	}
	row := domain.GitHubRepo{BotID: botID, TokenUserID: actor.ID, FullName: repo.FullName, Branch: in.Branch, RootDir: root,
		Private: repo.Private, AutoDeploy: in.AutoDeploy, CreatedAtMS: now, UpdatedAtMS: now}
	var secretPlain string
	if exists {
		row.SecretCipher, row.SecretNonce, row.SecretKeyID = cur.SecretCipher, cur.SecretNonce, cur.SecretKeyID
		row.LastSHA, row.LastDeployedMS, row.LastError, row.CreatedAtMS = cur.LastSHA, cur.LastDeployedMS, cur.LastError, cur.CreatedAtMS
		row.HookID = cur.HookID
		// A different repository (or token owner) invalidates the old hook.
		if cur.HookID != nil && (!strings.EqualFold(cur.FullName, repo.FullName) || !in.AutoDeploy) {
			if ot, err := d.OAuth.GitHubToken(ctx, cur.TokenUserID); err == nil {
				if err := d.GH.DeleteHook(ctx, ot, cur.FullName, *cur.HookID); err != nil {
					d.warn("remove old webhook", err)
				}
			}
			row.HookID = nil
		}
		if !strings.EqualFold(cur.FullName, repo.FullName) {
			row.LastSHA, row.LastDeployedMS, row.LastError = nil, nil, nil
		}
	} else {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return RepoView{}, err
		}
		secretPlain = hex.EncodeToString(raw)
		sl, err := d.Keys.Seal(secretNS(botID), secrets.GitHubSecretName, []byte(secretPlain))
		if err != nil {
			return RepoView{}, err
		}
		row.SecretCipher, row.SecretNonce, row.SecretKeyID = sl.Ciphertext, sl.Nonce, sl.KeyID
	}
	// The row (with its secret) must exist before GitHub sends its first ping.
	if err := d.store().UpsertGitHubRepo(ctx, row); err != nil {
		return RepoView{}, err
	}

	// A webhook needs a token that administers the repository and an address
	// GitHub can reach; anything else is polled.
	if in.AutoDeploy && row.HookID == nil && tok != "" && d.publicURL() != "" {
		secret, err := d.secret(row)
		if err != nil {
			return RepoView{}, err
		}
		id, err := d.GH.CreateHook(ctx, tok, repo.FullName, d.WebhookURL(), secret)
		if err != nil {
			// Not fatal: the branch is polled, and the owner of the repository
			// can still add the webhook by hand with the values shown.
			d.warn("create webhook", err)
			v := d.view(row)
			if !repo.Private || d.PollInterval < 0 {
				v.Secret = secret
			}
			return v, nil
		}
		row.HookID = &id
		row.UpdatedAtMS = d.now().UnixMilli()
		if err := d.store().UpsertGitHubRepo(ctx, row); err != nil {
			return RepoView{}, err
		}
	}
	v := d.view(row)
	if secretPlain != "" && in.AutoDeploy && row.HookID == nil && tok != "" && d.publicURL() != "" {
		v.Secret = secretPlain
	}
	return v, nil
}

// remoteReady refuses work for a node whose agent is not connected.
func (d *DeployService) remoteReady(nodeID string) error {
	if d.Remote == nil {
		return domain.Invalid("deploying from GitHub is not available for servers on remote nodes on this panel")
	}
	if !d.Remote.Online(nodeID) {
		return domain.Invalid("the server's node is offline; deploy again when its agent reconnects")
	}
	return nil
}

func (d *DeployService) secret(r domain.GitHubRepo) (string, error) {
	pt, err := d.Keys.Open(secretNS(r.BotID), secrets.GitHubSecretName, secrets.Sealed{Ciphertext: r.SecretCipher, Nonce: r.SecretNonce, KeyID: r.SecretKeyID})
	return string(pt), err
}

// Unlink removes the repository link and its webhook.
func (d *DeployService) Unlink(ctx context.Context, actor domain.User, botID string) error {
	if _, err := d.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin); err != nil {
		return err
	}
	// Unlinking wins over a deployment in progress.
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_ = d.Bots.Coord.Preempt(pctx, botID)
	cancel()
	d.removeHook(ctx, botID)
	return d.store().DeleteGitHubRepo(ctx, botID)
}

// removeHook deletes the GitHub webhook of a bot, best effort.
func (d *DeployService) removeHook(ctx context.Context, botID string) {
	r, err := d.store().GetGitHubRepo(ctx, botID)
	if err != nil || r.HookID == nil {
		return
	}
	if tok, err := d.OAuth.GitHubToken(ctx, r.TokenUserID); err == nil {
		if err := d.GH.DeleteHook(ctx, tok, r.FullName, *r.HookID); err != nil {
			d.warn("remove webhook", err)
		}
	}
}

// BeforeBotDelete is wired to BotService.BeforeDelete.
func (d *DeployService) BeforeBotDelete(ctx context.Context, botID string) { d.removeHook(ctx, botID) }

// CreateFromGitHub creates a bot (on the chosen node) and links it to a
// repository; the first deploy runs in the background. If linking fails the bot is deleted again.
// With startAfter, the bot is started once the first deployment succeeds.
func (d *DeployService) CreateFromGitHub(ctx context.Context, actor domain.User, in CreateBotInput, gh ConfigureInput, startAfter bool) (domain.Bot, RepoView, error) {
	in.SourceType, in.TemplateID = "github", nil
	if in.NodeID != "" && d.Bots.remote(in.NodeID) {
		// The first deployment goes straight to the node: it must be reachable.
		if err := d.remoteReady(in.NodeID); err != nil {
			return domain.Bot{}, RepoView{}, err
		}
	}
	b, err := d.Bots.Create(ctx, actor, in)
	if err != nil {
		return domain.Bot{}, RepoView{}, err
	}
	v, err := d.configure(ctx, actor, b.ID, gh)
	if err != nil {
		_ = d.Bots.Delete(ctx, actor, b.ID)
		return domain.Bot{}, RepoView{}, err
	}
	d.enqueue(b.ID, DeployRequest{Trigger: "initial", ActorID: &actor.ID, StartAfter: startAfter && d.Bots.Notifier != nil})
	return b, v, nil
}

// ---- deploying ----

// DeployRequest selects what to deploy. SHA empty = the branch head.
type DeployRequest struct {
	Trigger string // manual | push | initial | api
	ActorID *string
	SHA     string // a specific commit (redeploy/rollback)
	// StartAfter starts a stopped bot once this deployment succeeds (the
	// first deployment of a new bot).
	StartAfter bool
	// PushSHA is the commit a push event announced (informational: a push
	// always deploys the branch head when it runs).
	PushSHA string
}

// linkKey identifies a repository link; a pending push only runs for the
// link it arrived for.
func linkKey(r domain.GitHubRepo) string {
	return strings.ToLower(r.FullName) + "@" + r.Branch + ":" + r.RootDir
}

// deferPush remembers a push for a server whose node is offline instead of
// recording a failed deployment. It reports whether the push was deferred.
// The newest push replaces an older one; it runs (deploying the branch head
// at that time) when the node's agent reconnects.
func (d *DeployService) deferPush(ctx context.Context, b domain.Bot, repo domain.GitHubRepo, req DeployRequest) bool {
	if req.Trigger != "push" || req.SHA != "" || !d.Bots.remote(b.NodeID) || d.Remote == nil || d.Remote.Online(b.NodeID) {
		return false
	}
	sha := req.PushSHA
	if !github.ValidSHA(sha) {
		sha = ""
	}
	if err := d.store().SetPendingPush(ctx, b.ID, sha, linkKey(repo), d.now().UnixMilli()); err != nil {
		d.warn("remember a push for an offline node", err)
		return false
	}
	if d.Log != nil {
		d.Log.Info("push deferred until the node reconnects", "bot", b.ID, "node", b.NodeID)
	}
	// The node may have reconnected between the check and the write; its
	// reconnect hook could then have missed this push.
	if d.Remote.Online(b.NodeID) {
		go d.RunPendingPushes(context.WithoutCancel(ctx), b.NodeID)
	}
	return true
}

// RunPendingPushes deploys the pushes that arrived while a node was offline,
// once per server (the branch head, so the newest commit). A push whose
// repository link changed or was removed meanwhile, or whose server was
// deleted, is dropped; the usual checks (superseded, link changed, moved)
// still apply while it runs. Called when a node's agent connects.
func (d *DeployService) RunPendingPushes(ctx context.Context, nodeID string) int {
	if d.Remote == nil || !d.Remote.Online(nodeID) {
		return 0
	}
	pending, err := d.store().TakePendingPushes(ctx, nodeID)
	if err != nil {
		d.warn("read pending pushes", err)
		return 0
	}
	n := 0
	for _, p := range pending {
		repo, err := d.store().GetGitHubRepo(ctx, p.BotID)
		if err != nil {
			continue // unlinked meanwhile
		}
		if !repo.AutoDeploy || linkKey(repo) != p.Link {
			label := repo.FullName + "@" + repo.Branch
			op := d.Ops.Begin(ctx, OpStart{BotID: p.BotID, Kind: domain.OpDeploy, Trigger: "push", SourceLabel: &label})
			d.Ops.Finish(ctx, op, domain.OpCancelled, "superseded", "a push received while the node was offline was dropped: the repository link or auto-deploy setting changed", nil)
			continue
		}
		d.enqueue(p.BotID, DeployRequest{Trigger: "push", PushSHA: p.SHA})
		n++
	}
	return n
}

// Deploy starts a deployment of the configured branch (or of req.SHA) in the
// background.
func (d *DeployService) Deploy(ctx context.Context, actor domain.User, botID string, sha string) error {
	return d.DeployAs(ctx, actor, botID, sha, "manual")
}

// DeployAs is Deploy with an explicit trigger (manual, schedule, api).
func (d *DeployService) DeployAs(ctx context.Context, actor domain.User, botID, sha, trigger string) error {
	if _, err := d.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles); err != nil {
		return err
	}
	b, err := d.store().GetBot(ctx, botID)
	if err != nil {
		return err
	}
	if _, err := d.store().GetGitHubRepo(ctx, botID); err != nil {
		return err
	}
	if sha != "" && !github.ValidSHA(sha) {
		return domain.Invalid("choose a full commit id")
	}
	if d.Bots.remote(b.NodeID) {
		// The panel's disk is not involved; the archive limits bound what
		// the node stages (there is no free-space preflight on the node yet).
		if err := d.remoteReady(b.NodeID); err != nil {
			return err
		}
	} else if err := diskPreflight(d.Files, d.MinFreeDisk); err != nil {
		return err
	}
	if err := d.Bots.FilesBlocked(botID); err != nil {
		if what, _ := d.Bots.Coord.Active(botID); what != "A deployment" {
			return err
		}
	}
	id := actor.ID
	d.enqueue(botID, DeployRequest{Trigger: trigger, ActorID: &id, SHA: sha})
	return nil
}

// Preview compares the deployed commit with the branch head, so users can
// see what a deployment would change before starting it.
func (d *DeployService) Preview(ctx context.Context, actor domain.User, botID string) (github.Comparison, error) {
	if _, err := d.Bots.Authorize(ctx, actor, botID, domain.PermEditFiles); err != nil {
		return github.Comparison{}, err
	}
	repo, err := d.store().GetGitHubRepo(ctx, botID)
	if err != nil {
		return github.Comparison{}, err
	}
	tok, err := d.OAuth.GitHubToken(ctx, repo.TokenUserID)
	if err != nil && !(errors.Is(err, domain.ErrNotFound) && !repo.Private) {
		return github.Comparison{}, domain.Invalid("the GitHub account used for this deployment is no longer connected")
	}
	head, err := d.GH.BranchSHA(ctx, tok, repo.FullName, repo.Branch)
	if err != nil {
		return github.Comparison{}, ghError(err)
	}
	if repo.LastSHA == nil || *repo.LastSHA == head {
		return github.Comparison{HeadSHA: head, Commits: []github.Commit{}, Files: []github.ChangedFile{}}, nil
	}
	cmp, err := d.GH.Compare(ctx, tok, repo.FullName, *repo.LastSHA, head)
	return cmp, ghError(err)
}

type jobState struct {
	running bool
	next    *DeployRequest // the newest request that arrived while running
}

// enqueue runs a deployment, coalescing requests that arrive while one is
// running into a single follow-up that uses the newest request.
func (d *DeployService) enqueue(botID string, req DeployRequest) {
	d.mu.Lock()
	if d.jobs == nil {
		d.jobs = map[string]*jobState{}
	}
	j := d.jobs[botID]
	if j == nil {
		j = &jobState{}
		d.jobs[botID] = j
	}
	if j.running {
		r := req
		j.next = &r
		d.mu.Unlock()
		return
	}
	j.running = true
	d.mu.Unlock()

	if d.sem == nil {
		d.sem = make(chan struct{}, 2)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			d.sem <- struct{}{}
			d.run(d.ctx(), botID, req)
			<-d.sem
			d.mu.Lock()
			if j.next == nil || d.ctx().Err() != nil {
				j.running, j.next = false, nil
				delete(d.jobs, botID)
				d.mu.Unlock()
				return
			}
			req, j.next = *j.next, nil
			d.mu.Unlock()
		}
	}()
}

const deployTimeout = 15 * time.Minute

// errSuperseded ends a deployment because newer intent (Stop, Delete, Unlink
// or a changed repository link) replaced it.
type errSuperseded struct{ why string }

func (e errSuperseded) Error() string { return e.why }

func (d *DeployService) run(base context.Context, botID string, req DeployRequest) {
	ctx, cancel := context.WithTimeout(base, deployTimeout)
	defer cancel()
	repo, err := d.store().GetGitHubRepo(ctx, botID)
	if err != nil {
		return
	}
	bot, err := d.store().GetBot(ctx, botID)
	if err != nil || bot.DesiredState == domain.DesiredDeleted {
		return
	}
	if d.deferPush(ctx, bot, repo, req) {
		return
	}
	kind := domain.OpDeploy
	if req.SHA != "" {
		kind = domain.OpRollback
	}
	label := repo.FullName + "@" + repo.Branch
	op := d.Ops.Begin(ctx, OpStart{BotID: botID, Kind: kind, Trigger: req.Trigger, ActorID: req.ActorID, SourceLabel: &label})
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(base), 10*time.Second)
	defer fcancel()

	claim, cctx, err := d.Bots.Coord.Claim(ctx, botID, "A deployment", true)
	if err != nil {
		d.Ops.Finish(fctx, op, domain.OpFailed, "busy", err.Error(), nil)
		return
	}
	defer claim.Release()

	sha, n, err := d.deployOnce(cctx, bot.NodeID, repo, req.SHA, op)
	if err != nil {
		var sup errSuperseded
		if errors.As(err, &sup) || (cctx.Err() != nil && ctx.Err() == nil) {
			why := sup.why
			if why == "" {
				why = "cancelled by a newer action on this bot"
			}
			d.Ops.Finish(fctx, op, domain.OpCancelled, "superseded", why, nil)
			return
		}
		msg := deployMessage(err)
		d.warn("deploy failed", err)
		_ = d.store().RecordDeploy(fctx, botID, nil, &msg, d.now().UnixMilli())
		d.Ops.Finish(fctx, op, domain.OpFailed, "deploy_failed", msg, nil)
		if d.Alerts.Wants(fctx, botID, "deploy") {
			d.Alerts.Notify(fctx, bot.OwnerID, domain.NotifyDeploys, "❌ Deploy failed: "+bot.Name, label+": "+msg, BotLink(bot))
		}
		return
	}
	claim.Release() // deployOnce recorded the commit together with the files
	// A running bot restarts on the new code (this also re-runs its build
	// step). The condition is evaluated atomically against CURRENT intent, so
	// a Stop that arrived during the download is never undone.
	restarted, started := false, false
	if d.Bots.Notifier != nil {
		d.Ops.Stage(fctx, op, "Restarting")
		if nb, changed, err := d.store().RestartIfRunning(fctx, botID, d.now().UnixMilli()); err == nil && changed {
			restarted = true
			d.Bots.Bus.Publish(events.Status{BotID: botID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
				Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
			d.Bots.Notifier.Notify(botID)
		} else if err == nil && req.StartAfter {
			// The first deployment of a new bot: start it now that its files exist.
			nb, changed, err := d.store().SetDesiredWithin(fctx, botID, domain.DesiredRunning, false, d.now().UnixMilli(), d.Bots.Limits.NodeMemoryBytes)
			if err != nil {
				d.warn("start after the first deployment", err)
			} else if changed {
				started = true
				d.Bots.Bus.Publish(events.Status{BotID: botID, DesiredState: nb.DesiredState, ObservedState: nb.ObservedState,
					Generation: nb.Generation, ObservedGeneration: nb.ObservedGeneration})
				d.Bots.Notifier.Notify(botID)
			}
		}
	}
	msg := fmt.Sprintf("Deployed %s (%d files)", sha[:7], n)
	if restarted {
		msg += "; the bot is restarting on the new code"
	}
	if started {
		msg += "; the bot is starting"
	}
	d.Ops.Finish(fctx, op, domain.OpSucceeded, "", msg, map[string]any{"files": n, "sha": sha, "restarted": restarted, "started": started})
	if d.Alerts.Wants(fctx, botID, "deploy") {
		d.Alerts.Notify(fctx, bot.OwnerID, domain.NotifyDeploys, "✅ Deployed "+bot.Name,
			fmt.Sprintf("%s · %s · %d files (%s)", label, sha[:7], n, req.Trigger), BotLink(bot))
	}
}

const maxTarball = 1 << 30

// deployOnce downloads and applies one commit. Before the workspace changes,
// it re-reads the bot and its repository link: a deleted bot, a removed or
// changed link, or a cancelled claim stops the deployment without touching
// any file. Servers on remote nodes receive the tarball over the node
// connection; it is never unpacked on the panel host for them.
func (d *DeployService) deployOnce(ctx context.Context, nodeID string, repo domain.GitHubRepo, want string, op string) (string, int, error) {
	remote := d.Bots.remote(nodeID)
	if remote {
		if err := d.remoteReady(nodeID); err != nil {
			return "", 0, err
		}
	}
	d.Ops.Stage(ctx, op, "Resolving the commit")
	tok, err := d.OAuth.GitHubToken(ctx, repo.TokenUserID)
	if err != nil {
		if !repo.Private && errors.Is(err, domain.ErrNotFound) {
			tok = "" // public repository: works without a token
		} else {
			return "", 0, domain.Invalid("the GitHub account used for this deployment is no longer connected")
		}
	}
	sha := want
	if sha == "" {
		if sha, err = d.GH.BranchSHA(ctx, tok, repo.FullName, repo.Branch); err != nil {
			return "", 0, ghError(err)
		}
	}
	d.Ops.Source(ctx, op, sha, "")
	d.Ops.Stage(ctx, op, "Downloading "+sha[:7])
	body, err := d.GH.Tarball(ctx, tok, repo.FullName, sha)
	if err != nil {
		return "", 0, ghError(err)
	}
	defer body.Close()
	lim := d.Limits
	if lim.MaxBytes == 0 {
		lim = filesystem.DefaultBackupLimits
	}
	if remote {
		return d.deployRemote(ctx, nodeID, repo, sha, io.LimitReader(body, maxTarball), lim, op)
	}
	w, err := d.Files.Open(repo.BotID)
	if err != nil {
		return "", 0, err
	}
	defer w.Close()
	d.Ops.Stage(ctx, op, "Unpacking and replacing files")
	n, commit, err := w.DeployTarGzCommit(io.LimitReader(body, maxTarball), repo.RootDir, lim, func() error {
		// Runs after the archive is fully validated in staging and before
		// the first workspace entry is replaced.
		return d.stillWanted(ctx, nodeID, repo)
	})
	if err != nil {
		return sha, n, err
	}
	// Record the new commit before the previous files are dropped: a crash
	// in between rolls the files back to match the recorded commit.
	fctx, cancel := bg(ctx)
	defer cancel()
	if err := d.store().RecordDeploy(fctx, repo.BotID, &sha, nil, d.now().UnixMilli()); err != nil {
		if rerr := commit.Rollback(); rerr != nil {
			d.warn("roll back files after a failed record", rerr)
		}
		return sha, 0, err
	}
	if err := commit.Finish(); err != nil {
		d.warn("clean up previous files", err)
	}
	return sha, n, nil
}

// stillWanted refuses a deployment that newer intent replaced: a cancelled
// claim, a deleted bot, a removed or changed repository link, or a server
// that is no longer on the node the deployment targets.
func (d *DeployService) stillWanted(ctx context.Context, nodeID string, repo domain.GitHubRepo) error {
	if ctx.Err() != nil {
		return errSuperseded{"cancelled by a newer action on this bot"}
	}
	cur, err := d.store().GetBot(ctx, repo.BotID)
	if err != nil || cur.DesiredState == domain.DesiredDeleted {
		return errSuperseded{"the bot was deleted"}
	}
	now, err := d.store().GetGitHubRepo(ctx, repo.BotID)
	if err != nil {
		return errSuperseded{"the repository was unlinked"}
	}
	if !strings.EqualFold(now.FullName, repo.FullName) || now.Branch != repo.Branch || now.RootDir != repo.RootDir {
		return errSuperseded{"the repository link changed; deploy again to use the new settings"}
	}
	if cur.NodeID != nodeID {
		return errSuperseded{"the server moved to another node"}
	}
	return nil
}

// deployRemote applies one downloaded commit on the server's node. The node
// stages the whole archive first; the panel then re-checks the bot and its
// link, lets the node swap, records the commit and only then confirms. Any
// failure in between rolls the node's workspace back, and a confirmation
// that never arrives expires to rollback on the node.
func (d *DeployService) deployRemote(ctx context.Context, nodeID string, repo domain.GitHubRepo, sha string, src io.Reader, lim filesystem.BackupLimits, op string) (string, int, error) {
	if err := d.stillWanted(ctx, nodeID, repo); err != nil {
		return sha, 0, err // nothing was sent yet
	}
	d.Ops.Stage(ctx, op, "Sending to the node and unpacking")
	tx, err := d.Remote.BeginDeploy(ctx, nodeID, repo.BotID, src, repo.RootDir, lim)
	if err != nil {
		return sha, 0, err
	}
	complete := func(ok bool) error {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for {
			err := d.Remote.CompleteTransaction(fctx, nodeID, repo.BotID, tx, ok)
			var ve *domain.ValidationError
			if err == nil || errors.Is(err, domain.ErrNotFound) || errors.As(err, &ve) {
				return err // done, or an answer that a retry cannot change
			}
			select {
			case <-fctx.Done():
				return err
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	// The node has validated and staged everything; nothing changed yet.
	if err := d.stillWanted(ctx, nodeID, repo); err != nil {
		if cerr := complete(false); cerr != nil && !errors.Is(cerr, domain.ErrNotFound) {
			d.warn("abandon a staged remote deployment", cerr)
		}
		return sha, 0, err
	}
	d.Ops.Stage(ctx, op, "Replacing files")
	n, err := d.Remote.ApplyDeploy(ctx, nodeID, repo.BotID, tx)
	if err != nil {
		// A failed swap is rolled back by the node; a lost answer is rolled
		// back here (or by the node's expiry when it cannot be reached).
		if cerr := complete(false); cerr != nil && !errors.Is(cerr, domain.ErrNotFound) {
			d.warn("roll back a remote deployment after a failed swap", cerr)
		}
		return sha, 0, err
	}
	fctx, cancel := bg(ctx)
	defer cancel()
	if err := d.store().RecordDeploy(fctx, repo.BotID, &sha, nil, d.now().UnixMilli()); err != nil {
		if rerr := complete(false); rerr != nil {
			d.warn("roll back remote files after a failed record", rerr)
		}
		return sha, 0, err
	}
	if err := complete(true); err != nil {
		// The node keeps the previous files until it hears the commit and
		// rolls back when it never does. Put the recorded commit back so
		// the panel does not claim a deployment the node may have undone.
		d.warn("confirm a remote deployment", err)
		msg := "the node did not confirm the new files and rolls them back; deploy again"
		if repo.LastSHA != nil {
			_ = d.store().RecordDeploy(fctx, repo.BotID, repo.LastSHA, &msg, d.now().UnixMilli())
		}
		return sha, 0, domain.Invalid(msg)
	}
	return sha, n, nil
}

func deployMessage(err error) string {
	var ae *filesystem.ErrArchive
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ae):
		return ae.Msg
	case errors.As(err, &ve):
		return ve.Msg
	case errors.Is(err, context.DeadlineExceeded):
		return "the deployment timed out"
	}
	return "the deployment failed; see the panel logs"
}

func (d *DeployService) warn(msg string, err error) {
	if d.Log != nil {
		d.Log.Warn("deploy: "+msg, "err", err)
	}
}

// ---- webhook ----

// ErrBadSignature is returned for every webhook that cannot be authenticated,
// whether the repository is unknown or the signature is wrong, so callers learn
// nothing about which repositories are configured.
var ErrBadSignature = errors.New("invalid webhook signature")

func validSignature(secret string, body []byte, header string) bool {
	hexsig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	want, err := hex.DecodeString(hexsig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// HandleWebhook authenticates and processes a GitHub delivery. It returns a
// short result ("pong", "queued", "ignored") or ErrBadSignature.
func (d *DeployService) HandleWebhook(ctx context.Context, event, delivery, signature string, body []byte) (string, error) {
	var p struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &p) != nil || !github.ValidFullName(p.Repository.FullName) {
		return "", ErrBadSignature
	}
	cands, err := d.store().ListAutoDeployRepos(ctx, p.Repository.FullName)
	if err != nil {
		return "", err
	}
	var verified []domain.GitHubRepo
	for _, c := range cands {
		if sec, err := d.secret(c); err == nil && validSignature(sec, body, signature) {
			verified = append(verified, c)
		}
	}
	if len(verified) == 0 {
		return "", ErrBadSignature
	}
	switch event {
	case "ping":
		return "pong", nil
	case "push":
	default:
		return "ignored", nil
	}
	if delivery != "" && d.duplicate(delivery) {
		return "ignored", nil
	}
	branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
	if !ok || p.Deleted {
		return "ignored", nil
	}
	queued := false
	for _, c := range verified {
		// A commit the panel pushed from this bot's own files is already
		// deployed; redeploying it would only restart the bot.
		if c.Branch == branch && (c.LastSHA == nil || *c.LastSHA != p.After) {
			d.enqueue(c.BotID, DeployRequest{Trigger: "push", PushSHA: p.After})
			queued = true
		}
	}
	if !queued {
		return "ignored", nil
	}
	return "queued", nil
}

// duplicate remembers recent delivery ids so a GitHub redelivery does not
// deploy twice.
func (d *DeployService) duplicate(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[string]time.Time{}
	}
	now := d.now()
	if len(d.seen) > 2000 {
		for k, t := range d.seen {
			if now.Sub(t) > time.Hour {
				delete(d.seen, k)
			}
		}
	}
	if _, ok := d.seen[id]; ok {
		return true
	}
	d.seen[id] = now
	return false
}

// ---- polling ----

func (d *DeployService) pollEvery() time.Duration {
	if d.PollInterval > 0 {
		return d.PollInterval
	}
	return 5 * time.Minute
}

// pollLoop checks auto-deploy sources that have no webhook for new commits.
func (d *DeployService) pollLoop(ctx context.Context) {
	t := time.NewTicker(d.pollEvery())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.PollOnce(ctx)
		}
	}
}

// PollOnce checks every polled source once and queues a deployment for each
// branch whose head moved. A head is queued once, so a failing commit is not
// retried every interval.
func (d *DeployService) PollOnce(ctx context.Context) int {
	repos, err := d.store().ListPollingRepos(ctx, 500)
	if err != nil {
		d.warn("list polled repositories", err)
		return 0
	}
	queued := 0
	for _, r := range repos {
		if ctx.Err() != nil {
			return queued
		}
		if d.isRunning(r.BotID) {
			continue // a deployment is already fetching the branch head
		}
		if b, err := d.store().GetBot(ctx, r.BotID); err != nil {
			continue
		} else if d.Bots.remote(b.NodeID) && (d.Remote == nil || !d.Remote.Online(b.NodeID)) {
			continue // checked again once the node's agent reconnects
		}
		tok, err := d.OAuth.GitHubToken(ctx, r.TokenUserID)
		if err != nil {
			if r.Private || !errors.Is(err, domain.ErrNotFound) {
				continue
			}
			tok = ""
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		head, err := d.GH.BranchSHA(cctx, tok, r.FullName, r.Branch)
		cancel()
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				return queued // try again next interval
			}
			continue
		}
		d.mu.Lock()
		if d.polled == nil {
			d.polled = map[string]string{}
		}
		seen := d.polled[r.BotID] == head
		d.polled[r.BotID] = head
		d.mu.Unlock()
		if seen || (r.LastSHA != nil && *r.LastSHA == head) {
			continue
		}
		d.enqueue(r.BotID, DeployRequest{Trigger: "push"})
		queued++
	}
	return queued
}
