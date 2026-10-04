//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/agentclient"
	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/agentnode"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/api"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/pkgmgr"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/sftpd"
	"github.com/xenycx/rivetpanel/internal/templates"
)

// itSFTPAuth signs the test user in to SFTP as "it-sftp" / "it".
type itSFTPAuth struct{ u domain.User }

func (a itSFTPAuth) LoginSFTP(_ context.Context, email, secret string) (domain.User, func(context.Context) (domain.User, error), error) {
	if email != "it-sftp" || secret != "it" {
		return domain.User{}, nil, domain.ErrUnauthorized
	}
	return a.u, func(context.Context) (domain.User, error) { return a.u, nil }, nil
}

// TestRemoteAgentNode runs a panel and a rivet-agent in one process against
// the real Docker daemon: enrollment, mutual TLS, remote lifecycle, console,
// files, transactional backup/restore, GitHub deployment (manual and polled),
// deletion and certificate revocation.
func TestRemoteAgentNode(t *testing.T) {
	s := newStack(t) // the panel side: database, bot service, local runner
	ctx := s.ctx
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// --- panel: CA, hub listener, enrollment endpoint ---
	ca, err := agentcert.LoadOrCreate(filepath.Join(s.dir, "agent-ca"))
	if err != nil {
		t.Fatal(err)
	}
	enroll := &service.AgentEnrollmentService{Store: s.db, CA: ca}
	installID, _ := s.db.InstallationID(ctx, time.Now().UnixMilli())
	hub := &agenthub.Hub{Store: s.db, CA: ca, Env: s.bots, InstallID: installID, Log: log}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCert, err := ca.IssueServer([]string{"127.0.0.1", "localhost"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hubCtx, stopHub := context.WithCancel(ctx)
	defer stopHub()
	go hub.Serve(hubCtx, ln, serverCert)
	enroll.AgentAddress = ln.Addr().String()
	app := api.New(api.Deps{Log: log, DB: s.db, Enrollment: enroll})
	httpSrv := httptest.NewServer(adaptFiber(app))
	defer httpSrv.Close()

	router := &noderoute.Router{LocalNode: s.nodeID, Local: s.rn, LocalConsole: s.dk, LocalStats: s.dk, LocalStdin: s.dk, Hub: hub, Bots: s.db}
	s.bots.Notifier, s.bots.Purger, s.bots.Killer = router, router, router
	s.bots.RemoteNode = router.Remote

	plain, enrollment, err := enroll.Create(ctx, "edge-it", domain.LocalLocationID, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := enrollment.NodeID

	// --- agent: enroll, then serve with its own files and runner ---
	agentDir := filepath.Join(s.dir, "agent")
	cfg, err := agentclient.Enroll(ctx, httpSrv.Client(), httpSrv.URL, plain, agentDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(agentDir, agentclient.IdentityFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("node identity file: %v %v", fi, err)
	}
	if _, err := agentclient.Enroll(ctx, httpSrv.Client(), httpSrv.URL, plain, filepath.Join(s.dir, "agent2"), ""); err == nil {
		t.Fatal("the enrollment token worked twice")
	}
	files, err := filesystem.NewManager(filepath.Join(s.dir, "agent-servers"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	dk, err := docker.New(dockerHost(t))
	if err != nil {
		t.Fatal(err)
	}
	defer dk.Close()
	agent := agentclient.New(agentDir, cfg, nil, log)
	opts := s.opts
	opts.NodeID, opts.InstallID = nodeID, installID
	arn, err := runner.New(runner.Deps{Store: agent.Remote, Docker: dk, Env: agent.Remote, Workspaces: agentnode.Workspaces{Manager: files},
		Catalog: s.cat, Log: log, Builds: agent.Remote}, opts)
	if err != nil {
		t.Fatal(err)
	}
	agentAddonData := addons.DataRoot{Dir: filepath.Join(s.dir, "agent-addons")}
	agentDiag := &runner.Diagnostic{Docker: dk, Files: files, Catalog: s.cat, ScratchRoot: filepath.Join(s.dir, "agent-ai-scratch"),
		User: opts.User, InstallID: installID, NodeID: nodeID, Timeout: 5 * time.Minute}
	agent.Handler = agentnode.App(agentnode.Deps{Runner: arn, Addons: arn, AddonData: agentAddonData, Diagnostics: agentDiag,
		Docker: dk, Files: files, InstallID: func() string { return installID }, Log: log}).Handler()
	agent.Hello = func() agentproto.Hello {
		return agentproto.Hello{Protocol: agentproto.Version, AgentVersion: "it", Hostname: "it"}
	}
	agentCtx, stopAgent := context.WithCancel(ctx)
	defer stopAgent()
	go agent.Run(agentCtx)
	go arn.Run(agentCtx)
	s.waitFor("agent connected", 20*time.Second, func() bool { return hub.Connected(nodeID) })
	st, err := s.db.GetAgentState(ctx, nodeID)
	if err != nil || !st.Connected || st.AgentVersion != "it" {
		t.Fatalf("agent state %+v %v", st, err)
	}

	// --- a bot on the remote node (placing servers is an administrator's choice) ---
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = 'admin' WHERE id = ?`, s.user.ID); err != nil {
		t.Fatal(err)
	}
	s.user.Role = domain.RoleAdmin
	b, err := s.bots.Create(ctx, s.user, service.CreateBotInput{Name: "it-remote", Runtime: "nodejs", NodeID: nodeID,
		Argv: []string{"node", "index.js"}})
	if err != nil {
		t.Fatal(err)
	}
	lastID = b.ID
	if _, err := os.Stat(filepath.Join(s.dir, "bots", b.ID)); err == nil {
		t.Fatal("a remote server got a directory on the panel")
	}
	// Files go to the agent's disk through the connection.
	put, err := http.NewRequest(http.MethodPut, "/node/v1/bots/"+b.ID+"/files/content?path=index.js",
		strings.NewReader(`console.log("hello from the remote node"); setInterval(() => {}, 1000);`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hub.Forward(nodeID, put)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("write file through the agent: %v %v", resp, err)
	}
	resp.Body.Close()
	if data, err := files.ReadFile(b.ID, "index.js", 1<<10); err != nil || !bytes.Contains(data, []byte("remote node")) {
		t.Fatalf("file on the agent: %q %v", data, err)
	}

	// Backups are stored on the panel while their archive is streamed from the
	// assigned node. Restore swaps remotely, takes a safety copy and confirms
	// the agent transaction only after the panel database update.
	backupSvc := &service.BackupService{Bots: s.bots, Files: s.ws, Remote: router, Keys: s.bots.Keys,
		Dir: filepath.Join(s.dir, "backups"), Limits: filesystem.DefaultBackupLimits, Log: log}
	if err := backupSvc.Start(agentCtx); err != nil {
		t.Fatal(err)
	}
	s.bots.OnDelete = backupSvc.PurgeBot
	bk, err := backupSvc.Create(ctx, s.user, b.ID, service.CreateOptions{IncludeEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	s.waitFor("remote backup", time.Minute, func() bool {
		got, err := s.db.GetBackup(ctx, b.ID, bk.ID)
		return err == nil && got.Status == "ready"
	})
	if err := files.WriteFile(b.ID, "index.js", strings.NewReader(`console.log("changed after backup");`), 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := backupSvc.Restore(ctx, s.user, b.ID, bk.ID, service.RestoreOptions{RestoreEnv: true}); err != nil {
		t.Fatal(err)
	}
	if data, err := files.ReadFile(b.ID, "index.js", 1<<10); err != nil || !bytes.Contains(data, []byte("remote node")) {
		t.Fatalf("restored file on the agent: %q %v", data, err)
	}

	// GitHub deployment: the panel downloads the tarball from a deterministic
	// fake GitHub and streams it to the agent, which stages, swaps and keeps
	// the previous files until the panel has recorded the commit.
	gh := newFakeGitHub(t)
	deploySvc := &service.DeployService{Bots: s.bots, OAuth: &service.OAuthService{Store: s.db, Keys: s.bots.Keys}, Files: s.ws,
		GH: &github.Client{API: gh.srv.URL}, Keys: s.bots.Keys, Limits: filesystem.DefaultBackupLimits, Log: log,
		Ops: &service.Operations{Store: s.db, Bots: s.bots}, Remote: router, RemotePush: router, PollInterval: time.Hour}
	if s.bots.Coord == nil {
		s.bots.Coord = &service.Coordinator{}
	}
	deploySvc.Start(agentCtx)
	defer deploySvc.Wait()
	if _, err := deploySvc.Configure(ctx, s.user, b.ID, service.ConfigureInput{FullName: "o/r", Branch: "main", AutoDeploy: true}); err != nil {
		t.Fatal(err)
	}
	waitDeployed := func(sha string) {
		s.waitFor("remote deployment "+sha[:7], time.Minute, func() bool {
			r, err := s.db.GetGitHubRepo(ctx, b.ID)
			if err == nil && r.LastError != nil && *r.LastError != "" {
				t.Fatalf("remote deployment failed: %s", *r.LastError)
			}
			return err == nil && r.LastSHA != nil && *r.LastSHA == sha
		})
	}
	if err := deploySvc.DeployAs(ctx, s.user, b.ID, "", "manual"); err != nil {
		t.Fatal(err)
	}
	waitDeployed(gh.head())
	if data, err := files.ReadFile(b.ID, "lib/version.js", 1<<10); err != nil || string(data) != "module.exports = 1;" {
		t.Fatalf("deployed file on the agent: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "bots", b.ID)); err == nil {
		t.Fatal("a remote deployment was unpacked on the panel")
	}
	// Auto-deploy by polling (no public URL, so no webhook): a new branch head
	// reaches the agent too.
	gh.push("module.exports = 2;")
	if q := deploySvc.PollOnce(ctx); q != 1 {
		t.Fatalf("polling queued %d deployments", q)
	}
	waitDeployed(gh.head())
	if data, err := files.ReadFile(b.ID, "lib/version.js", 1<<10); err != nil || string(data) != "module.exports = 2;" {
		t.Fatalf("polled deployment on the agent: %q %v", data, err)
	}

	// GitHub push from the remote node (protocol 4): the agent selects the
	// files with the shared exclusions, the panel reads only those through the
	// connection and builds the commit; the pushed commit becomes last_sha so
	// polling does not redeploy it.
	sealed, err := s.bots.Keys.Seal("oauth:"+s.user.ID, domain.ProviderGitHub+":token", []byte(pushToken))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.UpsertOAuthAccount(ctx, domain.OAuthAccount{Provider: domain.ProviderGitHub, ProviderUserID: "42", UserID: s.user.ID,
		Username: "o", Scopes: "read:user repo", TokenCipher: sealed.Ciphertext, TokenNonce: sealed.Nonce, TokenKeyID: &sealed.KeyID,
		CreatedAtMS: time.Now().UnixMilli(), UpdatedAtMS: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{".env": "TOKEN=agent-secret", "node_modules/dep/index.js": "dep", "notes.txt": "pushed from the node"} {
		if err := files.WriteFile(b.ID, name, strings.NewReader(content), 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	// Pushing needs GitHub sign-in configured and the user's own token; a
	// second service instance shares the database and coordinator.
	pushSvc := &service.DeployService{Bots: s.bots, Files: s.ws, GH: &github.Client{API: gh.srv.URL}, Keys: s.bots.Keys, Log: log,
		OAuth: &service.OAuthService{Store: s.db, Keys: s.bots.Keys, Providers: map[string]oauth.Provider{
			"github": &oauth.GitHub{ClientID: "cid", Secret: "secret", APIBase: gh.srv.URL}}},
		Ops: &service.Operations{Store: s.db, Bots: s.bots}, RemotePush: router, PollInterval: -1}
	pushSvc.Start(agentCtx)
	defer pushSvc.Wait()
	plan, err := pushSvc.PushPlan(ctx, s.user, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var planned []string
	for _, f := range plan.Files {
		planned = append(planned, f.Path)
	}
	if got := strings.Join(planned, ","); got != "index.js,lib/version.js,notes.txt" {
		t.Fatalf("remote push plan = %s", got)
	}
	prevHead := gh.head()
	if err := pushSvc.Push(ctx, s.user, b.ID, "push from the remote node"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("remote push", time.Minute, func() bool { return gh.head() != prevHead })
	s.waitFor("pushed commit recorded", 30*time.Second, func() bool {
		r, err := s.db.GetGitHubRepo(ctx, b.ID)
		return err == nil && r.LastSHA != nil && *r.LastSHA == gh.head()
	})
	pushed := gh.headFiles()
	if len(pushed) != 3 || pushed["notes.txt"] != "pushed from the node" || pushed["lib/version.js"] != "module.exports = 2;" {
		t.Fatalf("pushed tree = %v", pushed)
	}
	if q := deploySvc.PollOnce(ctx); q != 0 {
		t.Fatalf("polling queued %d deployments for the commit the panel pushed", q)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "bots", b.ID)); err == nil {
		t.Fatal("a remote push created files on the panel")
	}

	if _, err := s.bots.Start(ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "running", 5*time.Minute)
	// The console streams through the agent.
	var out bytes.Buffer
	s.waitFor("remote console output", time.Minute, func() bool {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		rc, err := router.Console(nodeID).Logs(lctx, *b.ContainerID, time.Time{}, 50)
		if err != nil {
			return false
		}
		defer rc.Close()
		out.Reset()
		var stderr bytes.Buffer
		_, _ = stdcopy.StdCopy(&out, &stderr, rc)
		return strings.Contains(out.String(), "hello from the remote node")
	})
	// Listing files through the agent.
	var listing struct {
		Entries []struct{ Name string } `json:"entries"`
	}
	if err := hub.Call(ctx, nodeID, http.MethodGet, "/node/v1/bots/"+b.ID+"/files?path=.", nil, &listing); err != nil || len(listing.Entries) == 0 {
		t.Fatalf("list files through the agent: %+v %v", listing, err)
	}
	// The local runner never touches the remote server.
	if conts := s.containersOnNode(t, s.nodeID, b.ID); len(conts) != 0 {
		t.Fatalf("local runner created %d containers for a remote server", len(conts))
	}

	if _, err := s.bots.Stop(ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	s.waitObserved(b.ID, "stopped", 2*time.Minute)

	// --- package manifest edit and AI patch through the agent (protocol 5) ---
	s.bots.NodeFiles = router
	cur, err := s.db.GetBot(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	nf, remote, err := s.bots.RemoteFiles(cur, "The package manager")
	if err != nil || !remote {
		t.Fatalf("remote files for the remote server: %v %v", remote, err)
	}
	eco, err := pkgmgr.ForRuntime("nodejs", func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nf.WriteFile(ctx, nodeID, b.ID, "package.json", []byte("{\n  \"name\": \"it\"\n}\n"), "", true); err != nil {
		t.Fatal(err)
	}
	manifest, rev, err := nf.ReadFileRevision(ctx, nodeID, b.ID, "package.json", pkgmgr.MaxManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := eco.Apply(manifest, []pkgmgr.Op{{Action: "add", Name: "left-pad", Spec: "^1.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nf.WriteFile(ctx, nodeID, b.ID, "package.json", edited, rev, false); err != nil {
		t.Fatal(err)
	}
	if _, err := nf.WriteFile(ctx, nodeID, b.ID, "package.json", []byte("{}"), rev, false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale manifest write: %v", err)
	}
	if data, err := files.ReadFile(b.ID, "package.json", 1<<20); err != nil || !bytes.Contains(data, []byte(`"left-pad": "^1.3.0"`)) {
		t.Fatalf("manifest on the agent: %q %v", data, err)
	}
	tx, revs, err := nf.BeginPatch(ctx, nodeID, b.ID, []filesystem.PatchFile{{Path: "ai-note.txt", After: []byte("from the assistant\n")}}, 1<<20, 8<<20)
	if err != nil || revs["ai-note.txt"] == "" {
		t.Fatalf("remote patch: %v %v", revs, err)
	}
	if err := nf.CompleteTransaction(ctx, nodeID, b.ID, tx, true); err != nil {
		t.Fatal(err)
	}
	if data, err := files.ReadFile(b.ID, "ai-note.txt", 1<<10); err != nil || string(data) != "from the assistant\n" {
		t.Fatalf("patched file on the agent: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "bots", b.ID)); err == nil {
		t.Fatal("remote file edits created files on the panel")
	}

	// --- SFTP for the remote server goes through the agent (a stale
	// panel-local directory with the same id is never served) ---
	if err := s.ws.Create(b.ID); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(s.dir, "bots", b.ID, "decoy.txt")
	if err := os.WriteFile(decoy, []byte("stale panel copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	sftpSrvKey, err := sftpd.LoadOrCreateHostKey(filepath.Join(s.dir, "sftp-host"))
	if err != nil {
		t.Fatal(err)
	}
	sftpSrv := &sftpd.Server{Auth: itSFTPAuth{s.user}, Bots: s.bots, Files: s.ws, HostKey: sftpSrvKey, MaxFile: 4 << 20,
		SpoolDir: t.TempDir(), Log: log}
	sftpSrv.Remote = func(bot domain.Bot) (sftpd.NodeFiles, bool, error) {
		if !router.Remote(bot.NodeID) {
			return nil, false, nil
		}
		if _, _, err := s.bots.RemoteFiles(bot, "SFTP"); err != nil {
			return nil, true, err
		}
		return router, true, nil
	}
	sln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sftpCtx, stopSFTP := context.WithCancel(ctx)
	sftpDone := make(chan struct{})
	go func() { sftpSrv.Serve(sftpCtx, sln); close(sftpDone) }()
	sshc, err := ssh.Dial("tcp", sln.Addr().String(), &ssh.ClientConfig{User: "it-sftp", Auth: []ssh.AuthMethod{ssh.Password("it")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := sftp.NewClient(sshc)
	if err != nil {
		t.Fatal(err)
	}
	var sftpRoot string
	if infos, err := sc.ReadDir("/"); err == nil {
		for _, fi := range infos {
			if strings.HasSuffix(fi.Name(), "-"+b.ID[:8]) {
				sftpRoot = "/" + fi.Name()
			}
		}
	}
	if sftpRoot == "" {
		t.Fatal("the remote server is not listed over SFTP")
	}
	if _, err := sc.Open(sftpRoot + "/decoy.txt"); err == nil {
		t.Fatal("SFTP served the panel-local decoy of a remote server")
	}
	upload := bytes.Repeat([]byte("sftp through the agent\n"), 20000)
	wf, err := sc.Create(sftpRoot + "/sftp/upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wf.Write(upload); err != nil {
		t.Fatal(err)
	}
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := files.ReadFile(b.ID, "sftp/upload.txt", 4<<20); err != nil || !bytes.Equal(data, upload) {
		t.Fatalf("SFTP upload on the agent: %d bytes %v", len(data), err)
	}
	// rmdir is non-recursive on the node: a directory with a file is
	// refused and the file stays.
	if err := sc.RemoveDirectory(sftpRoot + "/sftp"); err == nil {
		t.Fatal("SFTP rmdir removed a non-empty directory on the agent")
	}
	if _, err := files.ReadFile(b.ID, "sftp/upload.txt", 4<<20); err != nil {
		t.Fatal("file lost after a refused rmdir:", err)
	}
	rf, err := sc.Open(sftpRoot + "/index.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rf)
	rf.Close()
	if err != nil || !bytes.Contains(got, []byte("remote node")) {
		t.Fatalf("SFTP download from the agent: %q %v", got, err)
	}
	if err := sc.Rename(sftpRoot+"/sftp/upload.txt", sftpRoot+"/sftp/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err := sc.Remove(sftpRoot + "/sftp/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err := sc.RemoveDirectory(sftpRoot + "/sftp"); err != nil {
		t.Fatal(err)
	}
	sc.Close()
	sshc.Close()
	stopSFTP()
	<-sftpDone
	if es, _ := os.ReadDir(filepath.Join(s.dir, "bots", b.ID)); len(es) != 1 {
		t.Fatalf("SFTP changed the panel-local directory of a remote server: %d entries", len(es))
	}
	if err := os.RemoveAll(filepath.Join(s.dir, "bots", b.ID)); err != nil {
		t.Fatal(err)
	}

	// --- add-on observation through the agent (protocol 6) ---
	if st, err := router.AddonStates(ctx, nodeID, b.ID); err != nil || len(st) != 0 {
		t.Fatalf("remote add-on states: %v %v", st, err)
	}
	if out, err := router.AddonLogs(ctx, nodeID, b.ID, "redis", 10); err != nil || out != "" {
		t.Fatalf("remote add-on logs without an add-on: %q %v", out, err)
	}

	// --- protocol 8: the node bind-tests host ports on its own host ---
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	heldPort := held.Addr().(*net.TCPAddr).Port
	probe, err := router.ProbePorts(ctx, nodeID, "0.0.0.0", []int{heldPort})
	held.Close()
	if err != nil || len(probe) != 1 || probe[0].Free || probe[0].Reason != "already in use on the node" {
		t.Fatalf("probe of a bound port: %+v %v", probe, err)
	}
	if probe, err = router.ProbePorts(ctx, nodeID, "0.0.0.0", []int{heldPort}); err != nil || !probe[0].Free {
		t.Fatalf("probe of a released port: %+v %v", probe, err)
	}

	// --- protocol 7: node telemetry, free-disk preflight, add-on data and
	// isolated AI diagnostics on the node's Docker ---
	tel, err := router.Telemetry(ctx, nodeID)
	if err != nil || tel.DiskTotalBytes == 0 || tel.DiskFreeBytes == 0 {
		t.Fatalf("node telemetry: %+v %v", tel, err)
	}
	if err := router.EnsureDiskFree(ctx, nodeID, 1<<20); err != nil {
		t.Fatalf("free-disk preflight: %v", err)
	}
	strict := *router
	strict.DiskMargin = int64(tel.DiskTotalBytes) + 1
	if _, err := strict.WriteFile(ctx, nodeID, b.ID, "too-big.bin", make([]byte, 2<<20), "", false); !errors.Is(err, noderoute.ErrNodeDiskFull) {
		t.Fatalf("staging on a full node: %v", err)
	}
	if _, err := files.ReadFile(b.ID, "too-big.bin", 4<<20); err == nil {
		t.Fatal("a refused file reached the node")
	}
	if p, err := agentAddonData.Ensure(b.ID, "redis", os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	} else if err := router.RemoveAddonData(ctx, nodeID, b.ID, "redis"); err != nil {
		t.Fatalf("remove add-on data on the node: %v", err)
	} else if _, err := os.Stat(p); err == nil {
		t.Fatal("add-on data still on the node")
	}
	if err := files.WriteFile(b.ID, "broken.js", strings.NewReader("function (\n"), 1<<20); err != nil {
		t.Fatal(err)
	}
	if res, err := router.RunDiagnostic(ctx, nodeID, b.ID, "nodejs", []string{"node", "--check", "index.js"}); err != nil || res.ExitCode != 0 {
		t.Fatalf("remote diagnostic: %+v %v", res, err)
	}
	if res, err := router.RunDiagnostic(ctx, nodeID, b.ID, "nodejs", []string{"node", "--check", "broken.js"}); err != nil || res.ExitCode == 0 || !strings.Contains(res.Output, "SyntaxError") {
		t.Fatalf("remote diagnostic of a broken file: %+v %v", res, err)
	}
	if _, err := router.RunDiagnostic(ctx, nodeID, b.ID, "nodejs", []string{"sh", "-c", "id"}); err == nil {
		t.Fatal("the node ran a command outside the runtime's allowlist")
	}
	if es, _ := os.ReadDir(filepath.Join(s.dir, "agent-ai-scratch")); len(es) != 0 {
		t.Fatalf("diagnostic snapshot left on the node: %d entries", len(es))
	}
	if err := router.RemoveOne(ctx, nodeID, b.ID, "broken.js"); err != nil {
		t.Fatal(err)
	}

	// --- template creation on the remote node: seeded through the agent in
	// one create-only transaction, never on the panel's disk ---
	tid := "discordjs"
	tb, err := s.bots.Create(ctx, s.user, service.CreateBotInput{Name: "it-remote-template", TemplateID: &tid, NodeID: nodeID})
	if err != nil {
		t.Fatalf("remote template creation: %v", err)
	}
	tfiles, err := templates.Files(tid)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range tfiles {
		if data, err := files.ReadFile(tb.ID, f.Path, 1<<20); err != nil || !bytes.Equal(data, f.Data) {
			t.Fatalf("template file %s on the agent: %v", f.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "bots", tb.ID)); err == nil {
		t.Fatal("a remote template was seeded on the panel")
	}
	// A verified Modrinth file is streamed to the node with its exact size
	// (the panel-side download and SHA-512 check are covered in internal/api).
	jar := bytes.Repeat([]byte("modrinth"), 64<<10)
	rev, err = router.WriteFileFrom(ctx, nodeID, tb.ID, "plugins/it.jar", bytes.NewReader(jar), int64(len(jar)), "", false)
	if n, ok := filesystem.RevisionSize(rev); err != nil || !ok || n != int64(len(jar)) {
		t.Fatalf("streamed plugin: %q %v", rev, err)
	}
	if data, err := files.ReadFile(tb.ID, "plugins/it.jar", 1<<20); err != nil || !bytes.Equal(data, jar) {
		t.Fatalf("plugin on the agent: %v", err)
	}
	if err := s.bots.Delete(ctx, s.user, tb.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("remote template server removed", time.Minute, func() bool {
		_, err := s.db.GetBot(ctx, tb.ID)
		return err != nil
	})

	if err := s.bots.Delete(ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("remote server removed", time.Minute, func() bool {
		_, err := s.db.GetBot(ctx, b.ID)
		return err != nil
	})
	if _, err := files.Path(b.ID); err == nil {
		t.Fatal("the agent kept the deleted server's files")
	}

	// --- revocation ends the connection and refuses reconnects ---
	if _, err := s.db.RevokeAgentCertificates(ctx, nodeID, "test", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	hub.Disconnect(nodeID)
	time.Sleep(3 * time.Second)
	if hub.Connected(nodeID) {
		t.Fatal("a revoked agent reconnected")
	}
	fmt.Fprintln(io.Discard)
}

// fakeGitHub serves the anonymous GitHub endpoints a public repository
// deployment uses, with a deterministic tarball per head.
type fakeGitHub struct {
	mu      sync.Mutex
	srv     *httptest.Server
	sha     string
	version string
	// Git Data write API (publish/push), authenticated with pushToken.
	blobs   map[string][]byte
	trees   map[string][]github.TreeEntry
	commits map[string]string // commit -> tree
}

const pushToken = "gho_integrationpushtoken"

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{sha: strings.Repeat("1", 40), version: "module.exports = 1;",
		blobs: map[string][]byte{}, trees: map[string][]github.TreeEntry{}, commits: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/git/", f.serveGit)
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"full_name": "o/r", "private": false, "default_branch": "main"})
	})
	mux.HandleFunc("/repos/o/r/commits/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sha": f.head()})
	})
	mux.HandleFunc("/repos/o/r/tarball/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		sha, version := f.sha, f.version
		f.mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/"+sha) {
			http.NotFound(w, r)
			return
		}
		files := map[string]string{
			"index.js":       `console.log("hello from the remote node"); setInterval(() => {}, 1000);`,
			"lib/version.js": version,
		}
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		top := "o-r-" + sha[:7] + "/"
		tw.WriteHeader(&tar.Header{Name: top, Typeflag: tar.TypeDir, Mode: 0o755})
		for _, n := range []string{"index.js", "lib/version.js"} {
			tw.WriteHeader(&tar.Header{Name: top + n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(files[n]))})
			tw.Write([]byte(files[n]))
		}
		tw.Close()
		gz.Close()
		w.Write(buf.Bytes())
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// serveGit is the Git Data API subset a push uses. Ids are deterministic.
func (f *fakeGitHub) serveGit(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+pushToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sub := strings.TrimPrefix(r.URL.Path, "/repos/o/r/git/")
	reply := func(code int, v any) { w.WriteHeader(code); json.NewEncoder(w).Encode(v) }
	var in map[string]json.RawMessage
	json.NewDecoder(r.Body).Decode(&in)
	str := func(k string) string { var v string; json.Unmarshal(in[k], &v); return v }
	id := func(parts ...string) string {
		h := sha1.New()
		for _, p := range parts {
			io.WriteString(h, p+"\x00")
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	switch {
	case sub == "ref/heads/main" && r.Method == http.MethodGet:
		reply(200, map[string]any{"object": map[string]string{"sha": f.sha}})
	case strings.HasPrefix(sub, "commits/") && r.Method == http.MethodGet:
		tree, ok := f.commits[strings.TrimPrefix(sub, "commits/")]
		if !ok {
			tree = "empty" // a deployed commit the fake did not create
		}
		reply(200, map[string]any{"tree": map[string]string{"sha": tree}})
	case strings.HasPrefix(sub, "trees/") && r.Method == http.MethodGet:
		reply(200, map[string]any{"tree": f.trees[strings.TrimPrefix(sub, "trees/")], "truncated": false})
	case sub == "blobs" && r.Method == http.MethodPost:
		b, err := base64.StdEncoding.DecodeString(str("content"))
		if err != nil {
			reply(422, map[string]string{"message": "bad content"})
			return
		}
		f.blobs[github.BlobSHA(b)] = b
		reply(201, map[string]string{"sha": github.BlobSHA(b)})
	case sub == "trees" && r.Method == http.MethodPost:
		var entries []github.TreeEntry
		json.Unmarshal(in["tree"], &entries)
		raw, _ := json.Marshal(entries)
		tree := id("tree", string(raw))
		f.trees[tree] = entries
		reply(201, map[string]string{"sha": tree})
	case sub == "commits" && r.Method == http.MethodPost:
		var parents []string
		json.Unmarshal(in["parents"], &parents)
		c := id("commit", str("tree"), strings.Join(parents, ","), str("message"))
		f.commits[c] = str("tree")
		reply(201, map[string]string{"sha": c})
	case sub == "refs/heads/main" && r.Method == http.MethodPatch:
		f.sha = str("sha") // the fake skips the fast-forward check
		reply(200, map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

// headFiles returns path -> content of the branch head's tree.
func (f *fakeGitHub) headFiles() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, e := range f.trees[f.commits[f.sha]] {
		out[e.Path] = string(f.blobs[e.SHA])
	}
	return out
}

func (f *fakeGitHub) head() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sha
}

func (f *fakeGitHub) push(version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sha, f.version = strings.Repeat("2", 40), version
}

func (s *stack) containersOnNode(t *testing.T, nodeID, botID string) []runner.ContainerInfo {
	t.Helper()
	all, err := s.dk.ListManaged(s.ctx, botID)
	if err != nil {
		t.Fatal(err)
	}
	var out []runner.ContainerInfo
	for _, c := range all {
		if c.Labels[runner.LabelNode] == nodeID {
			out = append(out, c)
		}
	}
	return out
}

var _ = json.Marshal

func adaptFiber(app *fiber.App) http.Handler { return adaptor.FiberApp(app) }
