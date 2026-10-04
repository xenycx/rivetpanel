package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

const workspaceMount = "/workspace"

// ContainerName is deterministic so a retried create after a lost response
// collides with (and rediscovers) the earlier container.
func ContainerName(botID string, role Role) string {
	return "rivetpanel-" + botID + "-" + string(role)
}

func envList(layers ...map[string]string) []string {
	merged := map[string]string{"HOME": workspaceMount}
	for _, l := range layers {
		for k, v := range l {
			merged[k] = v
		}
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// specHash covers the non-secret parts of the desired container. Environment
// values are deliberately excluded (they change generation instead), so the
// label never carries a fingerprint of a secret.
func specHash(role Role, imageRef string, argv []string, s ContainerSpec) string {
	b, _ := json.Marshal(struct {
		Role                Role
		Image               string
		Argv                []string
		User, Network, Host string
		Mem, CPU, Pids, Tmp int64
		Stdin               bool
		// omitempty keeps the hash of pre-existing bots unchanged.
		Entrypoint []string      `json:",omitempty"`
		Ports      []PortBinding `json:",omitempty"`
		Networks   []string      `json:",omitempty"`
	}{role, imageRef, argv, s.User, s.Network, s.WorkspaceHostPath, s.MemoryBytes, s.NanoCPUs, s.PidsLimit, s.TmpfsBytes, s.OpenStdin,
		s.Entrypoint, s.Ports, s.Networks})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

// runtimeSpec builds the execution container spec. image is the immutable
// reference actually used; the hash is computed from the catalog reference so
// it can be evaluated without contacting Docker.
func (r *Runner) runtimeSpec(bot domain.Bot, rt runtimes.Runtime, image, host string, botEnv map[string]string) ContainerSpec {
	network := r.opts.Network
	var extra []string
	if len(bot.Addons) > 0 {
		extra = []string{AddonNetwork(bot.ID)}
	}
	if bot.NetworkDisabled {
		network = "none" // no outbound access and, therefore, no published ports
		if len(extra) > 0 {
			// The internal add-on network has no route out either.
			network, extra = extra[0], nil
		}
	}
	provided, own := addonEnv(bot, botEnv)
	ports, env := portBindings(bot), envList(rt.Env, provided, own)
	if bot.IsGame() {
		// The panel's variables (memory, port) take precedence over the server's.
		if gs, ok := r.cachedGameSpec(bot); ok {
			ports, env = gamePorts(gs, bot), envList(provided, own, gameEnv(gs, bot))
		}
	}
	s := ContainerSpec{
		Name: ContainerName(bot.ID, RoleRuntime), Role: RoleRuntime, BotID: bot.ID, NodeID: bot.NodeID, InstallID: r.opts.InstallID,
		Generation: bot.Generation, Image: image, Argv: bot.Argv, Entrypoint: bot.Entrypoint, Ports: ports,
		Env: env, Networks: extra,
		WorkspaceHostPath: host, User: r.opts.User, Network: network,
		MemoryBytes: bot.MemoryBytes, NanoCPUs: bot.NanoCPUs, PidsLimit: bot.PidsLimit,
		TmpfsBytes: r.opts.TmpfsBytes, OpenStdin: true,
	}
	s.SpecHash = specHash(RoleRuntime, rt.ImageRef(), bot.Argv, s)
	return s
}

// wantHash is the spec hash of the runtime container the bot should have.
func (r *Runner) wantHash(bot domain.Bot, rt runtimes.Runtime, host string) string {
	return r.runtimeSpec(bot, rt, "", host, nil).SpecHash
}

func portBindings(bot domain.Bot) []PortBinding {
	if bot.NetworkDisabled {
		return nil
	}
	var out []PortBinding
	for _, p := range bot.Ports {
		out = append(out, PortBinding{HostIP: p.HostIP, HostPort: p.HostPort, ContainerPort: p.ContainerPort, Proto: p.Protocol})
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// builderSpec builds the dependency-install/compile container. It gets no bot
// secrets, only the catalog environment.
func (r *Runner) builderSpec(bot domain.Bot, rt runtimes.Runtime, image, host string) ContainerSpec {
	argv := rt.BuildArgv
	if bot.BuildCommand != "" {
		argv = BuildCommandArgv(bot.BuildCommand)
	}
	s := ContainerSpec{
		Name: ContainerName(bot.ID, RoleBuilder), Role: RoleBuilder, BotID: bot.ID, NodeID: bot.NodeID, InstallID: r.opts.InstallID,
		Generation: bot.Generation, Image: image, Argv: argv, Env: envList(rt.Env),
		WorkspaceHostPath: host, User: r.opts.User, Network: r.opts.Network,
		MemoryBytes: maxInt64(maxInt64(bot.MemoryBytes, r.opts.BuildMemory), rt.BuildMemoryBytes), NanoCPUs: maxInt64(bot.NanoCPUs, r.opts.BuildNanoCPUs),
		PidsLimit: maxInt64(bot.PidsLimit, r.opts.BuildPids), TmpfsBytes: r.opts.BuildTmpfsBytes,
	}
	s.SpecHash = specHash(RoleBuilder, rt.BuilderImage, argv, s)
	return s
}

// BuildCommandArgv runs a bot's custom build command. `set -e` stops at the
// first failing line, as people expect from a list of commands.
func BuildCommandArgv(script string) []string {
	return []string{"sh", "-c", "set -e\n" + script}
}

func describeExit(c ContainerInfo) string {
	if c.OOMKilled {
		return "killed: out of memory"
	}
	return fmt.Sprintf("exited with code %d", c.ExitCode)
}
