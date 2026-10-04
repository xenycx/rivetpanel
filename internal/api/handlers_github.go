package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/reposcan"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/templates"
)

func (s *panel) listTemplates(c fiber.Ctx) error {
	type tplDTO struct {
		templates.Template
		// From the runtime recipe, so the creation flow can show real needs.
		DefaultMemoryBytes int64 `json:"default_memory_bytes"`
		BuildMemoryBytes   int64 `json:"build_memory_bytes"`
		HasBuild           bool  `json:"has_build"`
	}
	list := templates.List()
	out := make([]tplDTO, 0, len(list))
	for _, t := range list {
		d := tplDTO{Template: t}
		if rt, ok := s.catalog.Get(t.Runtime); ok {
			d.DefaultMemoryBytes, d.BuildMemoryBytes, d.HasBuild = rt.Defaults.MemoryBytes, rt.BuildMemoryBytes, len(rt.BuildArgv) > 0
			if d.HasBuild && d.BuildMemoryBytes == 0 {
				d.BuildMemoryBytes = s.buildMemory
			}
		}
		out = append(out, d)
	}
	return c.JSON(fiber.Map{"templates": out})
}

type repoDTO struct {
	FullName       string `json:"full_name"`
	Branch         string `json:"branch"`
	RootDir        string `json:"root_dir"`
	Private        bool   `json:"private"`
	AutoDeploy     bool   `json:"auto_deploy"`
	HookCreated    bool   `json:"hook_created"`
	WebhookURL     string `json:"webhook_url"`
	Secret         string `json:"secret,omitempty"` // shown once, only for manual webhook setup
	LastSHA        string `json:"last_sha"`
	LastDeployedMS int64  `json:"last_deployed_at_ms"`
	LastError      string `json:"last_error"`
	Deploying      bool   `json:"deploying"`
	Polling        bool   `json:"polling"`
	PendingPushMS  int64  `json:"pending_push_at_ms,omitempty"`
}

func toRepo(v service.RepoView) repoDTO {
	return repoDTO{v.FullName, v.Branch, v.RootDir, v.Private, v.AutoDeploy, v.HookCreated, v.WebhookURL, v.Secret,
		v.LastSHA, v.LastDeployedMS, v.LastError, v.Deploying, v.Polling, v.PendingPushMS}
}

func (s *panel) githubRepos(c fiber.Ctx) error {
	rs, err := s.deploy.Repos(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"repos": rs})
}

func (s *panel) githubBranches(c fiber.Ctx) error {
	bs, err := s.deploy.Branches(c.Context(), currentUser(c), strings.Clone(c.Query("repo")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"branches": bs})
}

// githubLookup resolves a pasted repository address; public repositories
// need no GitHub connection.
func (s *panel) githubLookup(c fiber.Ctx) error {
	r, err := s.deploy.Lookup(c.Context(), currentUser(c), strings.Clone(c.Query("repo")))
	if err != nil {
		return err
	}
	return c.JSON(r)
}

// analyzeGitHub suggests how to host a repository (it creates nothing).
func (s *panel) analyzeGitHub(c fiber.Ctx) error {
	var in struct {
		Repo    string `json:"repo"`
		Branch  string `json:"branch"`
		RootDir string `json:"root_dir"`
		AI      bool   `json:"ai"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	a, err := s.deploy.Analyze(c.Context(), currentUser(c), service.AnalyzeInput{Repo: in.Repo, Branch: in.Branch, RootDir: in.RootDir, UseAI: in.AI})
	if err != nil {
		return err
	}
	return c.JSON(a)
}

// listRecipes returns the verified recipes for well-known open-source bots.
func (s *panel) listRecipes(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"recipes": reposcan.Recipes()})
}

func (s *panel) getGitHub(c fiber.Ctx) error {
	v, err := s.deploy.Get(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if errors.Is(err, domain.ErrNotFound) {
		if _, aerr := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermEditFiles); aerr == nil {
			return c.JSON(fiber.Map{"linked": false}) // the bot exists but has no repository
		}
	}
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"linked": true, "repo": toRepo(v)})
}

func (s *panel) putGitHub(c fiber.Ctx) error {
	var in githubIn
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.deploy.Configure(c.Context(), currentUser(c), strings.Clone(c.Params("id")),
		service.ConfigureInput{FullName: in.FullName, Branch: in.Branch, RootDir: in.RootDir, AutoDeploy: in.AutoDeploy})
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"linked": true, "repo": toRepo(v)})
}

func (s *panel) deleteGitHub(c fiber.Ctx) error {
	if err := s.deploy.Unlink(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) deployGitHub(c fiber.Ctx) error {
	var in struct {
		SHA string `json:"sha"` // optional: a specific commit (redeploy or roll back)
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	if err := s.deploy.Deploy(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(in.SHA)); err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "queued"})
}

// previewGitHub shows what deploying the branch head would change.
func (s *panel) previewGitHub(c fiber.Ctx) error {
	cmp, err := s.deploy.Preview(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(cmp)
}

// githubWebhook receives push events. It answers 401 for anything it cannot
// authenticate, without saying why.
func (s *panel) githubWebhook(c fiber.Ctx) error {
	body := append([]byte(nil), c.Body()...) // request memory is reused after the handler returns
	res, err := s.deploy.HandleWebhook(c.Context(), strings.Clone(c.Get("X-GitHub-Event")), strings.Clone(c.Get("X-GitHub-Delivery")),
		strings.Clone(c.Get("X-Hub-Signature-256")), body)
	if errors.Is(err, service.ErrBadSignature) {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid signature")
	}
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": res})
}

// githubOwners lists the accounts the caller can create repositories under.
func (s *panel) githubOwners(c fiber.Ctx) error {
	o, err := s.deploy.Owners(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"owners": o})
}

// pushPlan previews the files a publish or push would send.
func (s *panel) pushPlan(c fiber.Ctx) error {
	set, err := s.deploy.PushPlan(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	sample := set.Files
	if len(sample) > 300 {
		sample = sample[:300]
	}
	skipped := set.Skipped
	if skipped == nil {
		skipped = []filesystem.PushSkip{}
	}
	if sample == nil {
		sample = []filesystem.PushFile{}
	}
	return c.JSON(fiber.Map{"files": len(set.Files), "bytes": set.Bytes, "ignored": set.Ignored, "skipped": skipped,
		"sample": sample, "truncated": len(set.Files) > len(sample)})
}

// publishGitHub creates a repository from the bot's files and links it.
func (s *panel) publishGitHub(c fiber.Ctx) error {
	var in struct {
		Owner       string `json:"owner"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Private     bool   `json:"private"`
		AutoDeploy  bool   `json:"auto_deploy"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	repo, err := s.deploy.Publish(c.Context(), currentUser(c), strings.Clone(c.Params("id")),
		service.PublishInput{Owner: in.Owner, Name: in.Name, Description: in.Description, Private: in.Private, AutoDeploy: in.AutoDeploy})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "queued", "repo": repo})
}

// pushGitHub commits the bot's current files to its linked repository.
func (s *panel) pushGitHub(c fiber.Ctx) error {
	var in struct {
		Message string `json:"message"`
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	if err := s.deploy.Push(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Message); err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "queued"})
}
