package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

// GameStore is the persistence the runner needs for game servers. The main
// Store implements it; a store without it cannot run game servers.
type GameStore interface {
	GetBlueprintRevision(ctx context.Context, id string, rev int64) (domain.BlueprintRevision, error)
	SetInstallState(ctx context.Context, botID, state string, imageChoice *string, nowMS int64) error
	SetInstalledVersion(ctx context.Context, botID, version string) error
}

// GameFiles gives the runner contained file access for downloads and
// configuration edits. The workspace manager implements it.
type GameFiles interface {
	ReadFile(botID, rel string, max int64) ([]byte, error)
	WriteFile(botID, rel string, r io.Reader, max int64) error
}

const maxConfigFile = 4 << 20

func (r *Runner) gameStore() (GameStore, error) {
	gs, ok := r.store.(GameStore)
	if !ok {
		return nil, errors.New("this runner cannot run game servers")
	}
	return gs, nil
}

// gameSpec returns the pinned blueprint revision of a game server, parsed
// once per revision.
func (r *Runner) gameSpec(ctx context.Context, bot domain.Bot) (blueprint.Spec, error) {
	key := bot.BlueprintID + "@" + strconv.FormatInt(bot.BlueprintRevision, 10)
	r.mu.Lock()
	s, ok := r.blueprints[key]
	r.mu.Unlock()
	if ok {
		return s, nil
	}
	gs, err := r.gameStore()
	if err != nil {
		return blueprint.Spec{}, err
	}
	rev, err := gs.GetBlueprintRevision(ctx, bot.BlueprintID, bot.BlueprintRevision)
	if err != nil {
		return blueprint.Spec{}, err
	}
	s, err = blueprint.Parse([]byte(rev.SpecYAML))
	if err != nil {
		return blueprint.Spec{}, err
	}
	r.mu.Lock()
	if r.blueprints == nil {
		r.blueprints = map[string]blueprint.Spec{}
	}
	r.blueprints[key] = s
	r.mu.Unlock()
	return s, nil
}

// cachedGameSpec returns an already-parsed revision without I/O.
func (r *Runner) cachedGameSpec(bot domain.Bot) (blueprint.Spec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.blueprints[bot.BlueprintID+"@"+strconv.FormatInt(bot.BlueprintRevision, 10)]
	return s, ok
}

// gameImage is the server's chosen image, or the blueprint's first one.
func gameImage(s blueprint.Spec, bot domain.Bot) blueprint.Image {
	if im, ok := s.Image(bot.ImageChoice); ok {
		return im
	}
	return s.Images[0]
}

// runtimeFor returns the catalog runtime of a bot, or a runtime synthesized
// from the blueprint of a game server: its image, and its install script as
// the build step.
func (r *Runner) runtimeFor(ctx context.Context, bot domain.Bot) (runtimes.Runtime, bool, error) {
	if !bot.IsGame() {
		rt, ok := r.cat.Get(bot.Runtime)
		return rt, ok, nil
	}
	s, err := r.gameSpec(ctx, bot)
	if err != nil {
		return runtimes.Runtime{}, false, err
	}
	im := gameImage(s, bot)
	rt := runtimes.Runtime{ID: "game", DisplayName: s.Name, Image: im.Ref, BuilderImage: im.Ref}
	if s.Install.Image != "" {
		rt.BuilderImage = s.Install.Image
	}
	if s.Install.Script != "" {
		rt.BuildArgv = BuildCommandArgv(s.Install.Script)
	}
	return rt, true, nil
}

// gameEnv are the variables the panel provides to a game server. They take
// precedence over the server's own variables.
func gameEnv(s blueprint.Spec, bot domain.Bot) map[string]string {
	mib := bot.MemoryBytes >> 20
	heap := mib * int64(s.Resources.HeapPercent) / 100
	if heap < 64 {
		heap = 64
	}
	env := map[string]string{
		"SERVER_MEMORY": strconv.FormatInt(heap, 10),
		"SERVER_IP":     "0.0.0.0",
		"SERVER_ID":     bot.ID,
	}
	if p, ok := bot.PrimaryAllocation(); ok {
		port := p.Port
		if s.Ports.Container != 0 {
			port = s.Ports.Container
		}
		env["SERVER_PORT"] = strconv.Itoa(port)
	}
	for i, p := range extraAllocations(bot) {
		if i >= blueprint.MaxExtraPorts {
			break
		}
		env["SERVER_PORT_"+strconv.Itoa(i+1)] = strconv.Itoa(p.Port)
	}
	return env
}

// extraAllocations are the server's non-primary allocations in ascending
// port order: SERVER_PORT_1 is the lowest.
func extraAllocations(bot domain.Bot) []domain.Allocation {
	var out []domain.Allocation
	for _, a := range bot.Allocations {
		if !a.Primary {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// gamePorts publishes every allocation for TCP and UDP. The primary
// allocation maps to the blueprint's fixed container port when it has one.
func gamePorts(s blueprint.Spec, bot domain.Bot) []PortBinding {
	if bot.NetworkDisabled {
		return nil
	}
	var out []PortBinding
	for _, a := range bot.Allocations {
		cp := a.Port
		if a.Primary && s.Ports.Container != 0 {
			cp = s.Ports.Container
		}
		for _, proto := range []string{"tcp", "udp"} {
			out = append(out, PortBinding{HostIP: a.IP, HostPort: a.Port, ContainerPort: cp, Proto: proto})
		}
	}
	return out
}

// gameVars are the values used to render templates: the server's variables
// (falling back to blueprint defaults) and the panel-provided ones.
func gameVars(s blueprint.Spec, bot domain.Bot, own map[string]string) map[string]string {
	vars := s.Defaults()
	for k, v := range own {
		if _, declared := s.Variable(k); declared {
			vars[k] = v
		}
	}
	for k, v := range gameEnv(s, bot) {
		vars[k] = v
	}
	return vars
}

// installGame runs a game server's installation: the provider download with
// automatic Java selection, then the blueprint's install script in a
// container. It returns the runtime to use afterwards (the image may change).
func (r *Runner) installGame(ctx context.Context, bot domain.Bot, rt runtimes.Runtime, host string, st *botState, op string) (runtimes.Runtime, string, error) {
	gs, err := r.gameStore()
	if err != nil {
		return rt, "game servers are unavailable", err
	}
	s, err := r.gameSpec(ctx, bot)
	if err != nil {
		return rt, "blueprint is unavailable", err
	}
	files, ok := r.ws.(GameFiles)
	if !ok {
		return rt, "server files are unavailable", errors.New("workspaces do not support file access")
	}
	_ = gs.SetInstallState(ctx, bot.ID, domain.InstallInstalling, nil, r.now().UnixMilli())
	own, err := r.env.DecryptEnv(ctx, bot.ID)
	if err != nil {
		return rt, "variables could not be decrypted", err
	}
	vars := gameVars(s, bot, own)
	log := r.buildLog(op)
	defer log.Close()
	choice := bot.ImageChoice
	version := ""

	if d := s.Install.Download; d != nil {
		r.buildStageName(ctx, op, "Finding the download")
		art, err := r.providers().Resolve(ctx, *d, vars)
		if err != nil {
			if errors.Is(err, blueprint.ErrUnknownVersion) {
				return rt, "installation failed: that version is not available", err
			}
			return rt, "installation failed: the download could not be found", err
		}
		fmt.Fprintf(log, "Installing %s %s\n", s.Name, art.Version)
		version = art.Version
		if choice == "" { // automatic image: pick the Java the version needs
			need := art.JavaHint
			if need == 0 && s.JavaFrom != "" {
				need = r.providers().JavaFor(ctx, d.Name, d.Project, art.Version)
			}
			choice = s.ImageForJava(need).Label
			if need > 0 {
				fmt.Fprintf(log, "Minecraft %s needs Java %d: using %s\n", art.Version, need, choice)
			}
		}
		dest := blueprint.Render(d.Dest, vars)
		if !validRel(dest) {
			return rt, "installation failed: the download destination is not a valid file name", errors.New("bad destination")
		}
		r.buildStageName(ctx, op, "Downloading "+art.Name)
		fmt.Fprintf(log, "Downloading %s\n", art.URL)
		pr, pw := io.Pipe()
		go func() {
			_, err := r.providers().Fetch(ctx, art, pw)
			pw.CloseWithError(err)
		}()
		if err := files.WriteFile(bot.ID, dest, pr, blueprint.MaxDownloadBytes); err != nil {
			pr.CloseWithError(err)
			if strings.Contains(err.Error(), "checksum") {
				return rt, "installation failed: the download did not match its published checksum", err
			}
			return rt, "installation failed: the download did not complete", err
		}
		fmt.Fprintf(log, "Saved %s\n", dest)
	}
	if choice == "" {
		choice = s.Images[0].Label
	}
	bot.ImageChoice = choice
	if rt, _, err = r.runtimeFor(ctx, bot); err != nil {
		return rt, "blueprint is unavailable", err
	}
	for _, a := range s.Agreements {
		if own[agreementVar(a.ID)] == "accepted" {
			if err := files.WriteFile(bot.ID, a.File, strings.NewReader(a.Content), maxConfigFile); err != nil {
				return rt, "installation failed: could not write " + a.File, err
			}
		}
	}
	if err := r.ws.Prepare(bot.ID, r.uid, r.gid); err != nil {
		return rt, "workspace ownership could not be prepared", err
	}
	if sc := s.Install.SteamCMD; sc != nil {
		beta := strings.TrimSpace(blueprint.Render(sc.Beta, vars))
		srt, err := steamRuntime(*sc, beta)
		if err != nil {
			return rt, "installation failed: " + err.Error(), err
		}
		fmt.Fprintf(log, "Installing Steam app %d with SteamCMD (anonymous login)\n", sc.AppID)
		if beta != "" {
			fmt.Fprintf(log, "Beta branch: %s\n", beta)
		}
		r.buildStageName(ctx, op, "Downloading with SteamCMD")
		if msg, err := r.buildStage(ctx, bot, srt, host, st, op, log); err != nil {
			return rt, strings.Replace(strings.Replace(msg, "build", "SteamCMD installation", 1), "Build", "SteamCMD installation", 1), err
		}
		if version == "" {
			version = "app " + strconv.FormatInt(sc.AppID, 10)
			if m, err := files.ReadFile(bot.ID, "steamapps/appmanifest_"+strconv.FormatInt(sc.AppID, 10)+".acf", 1<<20); err == nil {
				if id := ACFValue(m, "buildid"); id != "" {
					version = "build " + id
				}
			}
			if beta != "" {
				version += " (" + beta + ")"
			}
			fmt.Fprintf(log, "Installed %s\n", version)
		}
	}
	if len(rt.BuildArgv) > 0 {
		fmt.Fprintln(log, "Running the install script")
		r.buildStageName(ctx, op, "Preparing the install image")
		irt := rt
		irt.Env = installEnv(s, vars)
		if msg, err := r.buildStage(ctx, bot, irt, host, st, op, log); err != nil {
			return rt, strings.Replace(msg, "build", "installation", 1), err
		}
	}
	if version != "" {
		_ = gs.SetInstalledVersion(ctx, bot.ID, version)
	}
	if err := gs.SetInstallState(ctx, bot.ID, domain.InstallInstalled, &choice, r.now().UnixMilli()); err != nil {
		return rt, "installation state could not be saved", err
	}
	st.built, st.builtGen = true, bot.Generation
	return rt, "", nil
}

// installEnv is the install script's environment: the server's declared
// variables and the panel-provided ones. Hidden RIVET_* values (accepted
// agreements) and undeclared variables are not passed.
func installEnv(s blueprint.Spec, vars map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range vars {
		if _, declared := s.Variable(k); declared || blueprint.IsSystemVariable(k) {
			out[k] = v
		}
	}
	return out
}

// AgreementVar is the hidden variable recording an accepted agreement.
func agreementVar(id string) string {
	return "RIVET_AGREEMENT_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}

// AgreementVar is exported for the service that records acceptance.
func AgreementVar(id string) string { return agreementVar(id) }

func validRel(p string) bool {
	return p != "" && !strings.ContainsAny(p, "\\\x00") && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "../") &&
		p != ".." && !strings.Contains(p, "/../")
}

// applyGameConfig edits the blueprint's configuration files before a start.
func (r *Runner) applyGameConfig(ctx context.Context, bot domain.Bot, own map[string]string) error {
	s, err := r.gameSpec(ctx, bot)
	if err != nil {
		return err
	}
	files, ok := r.ws.(GameFiles)
	if !ok {
		return errors.New("workspaces do not support file access")
	}
	vars := gameVars(s, bot, own)
	for _, c := range s.ConfigFiles {
		cur, err := files.ReadFile(bot.ID, c.Path, maxConfigFile)
		if err != nil {
			if !c.Create {
				continue // created by the server on its first start
			}
			cur = nil
		}
		out, err := blueprint.ApplyConfig(cur, c, vars)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Path, err)
		}
		if string(out) == string(cur) {
			continue
		}
		if err := files.WriteFile(bot.ID, c.Path, strings.NewReader(string(out)), maxConfigFile); err != nil {
			return fmt.Errorf("%s: %w", c.Path, err)
		}
	}
	for _, a := range s.Agreements {
		if own[agreementVar(a.ID)] != "accepted" {
			continue
		}
		cur, _ := files.ReadFile(bot.ID, a.File, maxConfigFile)
		if string(cur) != a.Content {
			if err := files.WriteFile(bot.ID, a.File, strings.NewReader(a.Content), maxConfigFile); err != nil {
				return fmt.Errorf("%s: %w", a.File, err)
			}
		}
	}
	return nil
}

// gracefulStop writes the blueprint's stop command to the server console (or
// sends its stop signal) and waits for it to exit, before the regular stop.
func (r *Runner) gracefulStop(ctx context.Context, bot domain.Bot, c ContainerInfo) {
	s, err := r.gameSpec(ctx, bot)
	if err != nil {
		return
	}
	if s.Startup.StopSignal != "" {
		sg, ok := r.docker.(interface {
			Signal(ctx context.Context, id, sig string) error
		})
		if !ok || sg.Signal(ctx, c.ID, s.Startup.StopSignal) != nil {
			return
		}
		wctx, cancel := context.WithTimeout(ctx, time.Duration(s.Startup.StopTimeoutSeconds)*time.Second)
		defer cancel()
		_, _ = r.docker.Wait(wctx, c.ID)
		return
	}
	if s.Startup.Stop == "" {
		return
	}
	att, ok := r.docker.(interface {
		AttachStdin(ctx context.Context, id string) (io.WriteCloser, error)
	})
	if !ok {
		return
	}
	in, err := att.AttachStdin(ctx, c.ID)
	if err != nil {
		return
	}
	_, werr := io.WriteString(in, s.Startup.Stop+"\n")
	in.Close()
	if werr != nil {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, time.Duration(s.Startup.StopTimeoutSeconds)*time.Second)
	defer cancel()
	_, _ = r.docker.Wait(wctx, c.ID)
}

func (r *Runner) providers() *blueprint.Providers {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prov == nil {
		r.prov = &blueprint.Providers{}
	}
	return r.prov
}

// buildLog returns a writer for the operation's retained output.
func (r *Runner) buildLog(op string) io.WriteCloser {
	if r.builds != nil && op != "-" && op != "" {
		if w := r.builds.BuildOutput(op); w != nil {
			return w
		}
	}
	return nopWriteCloser{io.Discard}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
