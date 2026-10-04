package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// AddonData removes and measures add-on data directories (addons.DataRoot).
type AddonData interface {
	Remove(botID, kind string) error
	Usage(botID, kind string) int64
}

// AddonState is the observed state of one add-on container.
type AddonState struct {
	State    string `json:"state"`  // running, exited, created, ... ("" = no container)
	Health   string `json:"health"` // starting, healthy, unhealthy or ""
	ExitCode int    `json:"exit_code"`
}

// AddonRuntime observes add-on containers (the runner). Optional.
type AddonRuntime interface {
	AddonStates(ctx context.Context, botID string) (map[string]AddonState, error)
	AddonLogs(ctx context.Context, botID, kind string, lines int) (string, error)
}

// NodeAddons observes the add-on containers of servers on remote nodes
// through their rivet-agent. Optional: without it remote add-on logs are
// refused and their states are unknown.
type NodeAddons interface {
	AddonStates(ctx context.Context, nodeID, botID string) (map[string]AddonState, error)
	AddonLogs(ctx context.Context, nodeID, botID, kind string, lines int) (string, error)
	// RemoveAddonData deletes one add-on's data on the node.
	RemoveAddonData(ctx context.Context, nodeID, botID, kind string) error
}

// AddonInput selects an add-on to attach. MemoryBytes 0 uses the kind's default.
type AddonInput struct {
	Kind        string
	MemoryBytes int64
}

// AddonView describes an attached add-on.
type AddonView struct {
	Kind        string     `json:"kind"`
	DisplayName string     `json:"display_name"`
	Description string     `json:"description"`
	Image       string     `json:"image"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	MemoryBytes int64      `json:"memory_bytes"`
	Variables   []string   `json:"variables"`
	DataBytes   int64      `json:"data_bytes"`
	CreatedAtMS int64      `json:"created_at_ms"`
	Status      AddonState `json:"status"`
}

const maxAddonsPerBot = 4

// addonsAvailable reports whether add-ons can run where a server on nodeID
// runs: this panel's Docker runner for its own node, or the node's agent for
// a remote node. Every built-in add-on kind is supported on agent nodes (the
// agent's runner uses the same add-on definitions, private network and a data
// directory beside its server files); the add-on's data then lives on the
// node and is managed through its agent.
func (s *BotService) addonsAvailable(nodeID string) error {
	if s.remote(nodeID) {
		if s.RemoteAddons == nil {
			return domain.Invalid("add-ons are not available for servers on remote nodes on this panel")
		}
		return nil
	}
	if s.AddonData == nil {
		return domain.Invalid("add-ons are not available on this panel (no Docker runner)")
	}
	return nil
}

// addonSupportedOnNode refuses an add-on kind a remote node cannot run. All
// built-in kinds qualify today; the check keeps a future kind that needs
// panel-only facilities from being attached to a remote server.
func addonSupportedOnNode(k addons.Kind) error {
	if k.Image == "" || k.DataPath == "" {
		return domain.Invalid(k.DisplayName + " is not available for servers on remote nodes")
	}
	return nil
}

// clearAddonData deletes data an earlier add-on of this kind may have left
// (it cannot be opened with a new password): on the node for a remote
// server, which must then be online, otherwise on this panel.
func (s *BotService) clearAddonData(ctx context.Context, b domain.Bot, kind string) error {
	if s.remote(b.NodeID) {
		if _, _, err := s.RemoteFiles(b, "Add-ons"); err != nil {
			return err
		}
		return s.RemoteAddons.RemoveAddonData(ctx, b.NodeID, b.ID, kind)
	}
	return s.AddonData.Remove(b.ID, kind)
}

func addonView(a domain.BotAddon, k addons.Kind) AddonView {
	return AddonView{Kind: k.ID, DisplayName: k.DisplayName, Description: k.Description, Image: k.Image, Host: k.ID, Port: k.Port,
		MemoryBytes: a.MemoryBytes, Variables: k.VarNames, CreatedAtMS: a.CreatedAtMS}
}

// Addons lists a bot's add-ons with their current state.
func (s *BotService) Addons(ctx context.Context, actor domain.User, botID string) ([]AddonView, error) {
	b, err := s.loadPerm(ctx, actor, botID, permAny, false)
	if err != nil {
		return nil, err
	}
	var states map[string]AddonState
	remote := s.remote(b.NodeID)
	switch {
	case len(b.Addons) == 0:
	case remote && s.RemoteAddons != nil:
		states, _ = s.RemoteAddons.AddonStates(ctx, b.NodeID, b.ID) // offline: state unknown
	case !remote && s.AddonRuntime != nil:
		states, _ = s.AddonRuntime.AddonStates(ctx, b.ID)
	}
	out := make([]AddonView, 0, len(b.Addons))
	for _, a := range b.Addons {
		k, ok := addons.Get(a.Kind)
		if !ok {
			out = append(out, AddonView{Kind: a.Kind, DisplayName: a.Kind + " (no longer available)", MemoryBytes: a.MemoryBytes})
			continue
		}
		v := addonView(a, k)
		v.Status = states[a.Kind]
		if s.AddonData != nil && !remote { // a remote add-on's data is on its node
			v.DataBytes = s.AddonData.Usage(b.ID, a.Kind)
		}
		out = append(out, v)
	}
	return out, nil
}

// AddAddon attaches an add-on to a stopped bot. It starts with the bot.
func (s *BotService) AddAddon(ctx context.Context, actor domain.User, botID string, in AddonInput) (AddonView, error) {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermFullAdmin, false)
	if err != nil {
		return AddonView{}, err
	}
	if err := s.Coord.Blocked(b.ID); err != nil {
		return AddonView{}, err
	}
	a, k, err := s.addAddon(ctx, b, in, true, false)
	if err != nil {
		return AddonView{}, err
	}
	return addonView(a, k), nil
}

// addAddon attaches an add-on. fresh is a server created in this request:
// no data of an earlier add-on can exist for it.
func (s *BotService) addAddon(ctx context.Context, b domain.Bot, in AddonInput, budget, fresh bool) (domain.BotAddon, addons.Kind, error) {
	if err := s.addonsAvailable(b.NodeID); err != nil {
		return domain.BotAddon{}, addons.Kind{}, err
	}
	k, ok := addons.Get(strings.ToLower(strings.TrimSpace(in.Kind)))
	if !ok {
		return domain.BotAddon{}, addons.Kind{}, domain.Invalid(fmt.Sprintf("unknown add-on %q", in.Kind))
	}
	if s.remote(b.NodeID) {
		if err := addonSupportedOnNode(k); err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
	}
	if len(b.Addons) >= maxAddonsPerBot {
		return domain.BotAddon{}, addons.Kind{}, domain.Invalid(fmt.Sprintf("a bot can have at most %d add-ons", maxAddonsPerBot))
	}
	mem := in.MemoryBytes
	if mem == 0 {
		mem = k.DefaultMemory
	}
	if err := k.ValidateMemory(mem); err != nil {
		return domain.BotAddon{}, addons.Kind{}, domain.Invalid(err.Error())
	}
	if budget {
		owner, err := s.Store.GetUserByID(ctx, b.OwnerID)
		if err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
		if err := s.checkUserBudget(ctx, owner, 0, mem); err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
	}
	a := domain.BotAddon{BotID: b.ID, Kind: k.ID, MemoryBytes: mem, CreatedAtMS: s.now(), UpdatedAtMS: s.now()}
	var pw *domain.EnvVar
	if k.Password {
		raw := make([]byte, 18)
		if _, err := rand.Read(raw); err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
		name := domain.AddonPasswordVar(k.ID)
		sl, err := s.Keys.Seal(b.ID, name, []byte(hex.EncodeToString(raw)))
		if err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
		pw = &domain.EnvVar{BotID: b.ID, Name: name, Ciphertext: sl.Ciphertext, Nonce: sl.Nonce, KeyID: sl.KeyID}
	}
	// A previous add-on of this kind may have left data behind (it was
	// removed with "keep data"); a new password cannot open it.
	if !fresh {
		if err := s.clearAddonData(ctx, b, k.ID); err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
	} else if !s.remote(b.NodeID) {
		if err := s.AddonData.Remove(b.ID, k.ID); err != nil {
			return domain.BotAddon{}, addons.Kind{}, err
		}
	}
	if err := s.Store.CreateBotAddon(ctx, a, pw, s.now()); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.BotAddon{}, addons.Kind{}, domain.Invalid(k.DisplayName + " is already attached to this bot")
		}
		return domain.BotAddon{}, addons.Kind{}, err
	}
	return a, k, nil
}

// SetAddonMemory changes an add-on's memory limit (stopped bots only).
func (s *BotService) SetAddonMemory(ctx context.Context, actor domain.User, botID, kind string, memory int64) error {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermFullAdmin, false)
	if err != nil {
		return err
	}
	k, ok := addons.Get(kind)
	if !ok {
		return domain.ErrNotFound
	}
	if err := k.ValidateMemory(memory); err != nil {
		return domain.Invalid(err.Error())
	}
	for _, a := range b.Addons {
		if a.Kind == kind && memory > a.MemoryBytes {
			owner, err := s.Store.GetUserByID(ctx, b.OwnerID)
			if err != nil {
				return err
			}
			if err := s.checkUserBudget(ctx, owner, 0, memory-a.MemoryBytes); err != nil {
				return err
			}
		}
	}
	return s.Store.UpdateBotAddonMemory(ctx, b.ID, kind, memory, s.now())
}

// RemoveAddon detaches an add-on from a stopped bot and deletes its data.
// The runner removes its container on the bot's next start or reconcile.
func (s *BotService) RemoveAddon(ctx context.Context, actor domain.User, botID, kind string) error {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermFullAdmin, false)
	if err != nil {
		return err
	}
	if err := s.addonsAvailable(b.NodeID); err != nil {
		return err
	}
	if err := s.Coord.Blocked(b.ID); err != nil {
		return err
	}
	remote := s.remote(b.NodeID)
	if remote {
		// The data is on the node: refuse while it is offline rather than
		// leave it behind.
		if _, _, err := s.RemoteFiles(b, "Add-ons"); err != nil {
			return err
		}
	}
	if err := s.Store.DeleteBotAddon(ctx, b.ID, kind, s.now()); err != nil {
		return err
	}
	if s.Notifier != nil {
		s.Notifier.Notify(b.ID) // removes the stopped add-on container
	}
	if remote {
		return s.RemoteAddons.RemoveAddonData(ctx, b.NodeID, b.ID, kind)
	}
	return s.AddonData.Remove(b.ID, kind)
}

// AddonConnection reveals the variables the bot receives for an add-on
// (they include its password).
func (s *BotService) AddonConnection(ctx context.Context, actor domain.User, botID, kind string) (map[string]string, error) {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermManageEnv, false)
	if err != nil {
		return nil, err
	}
	for _, a := range b.Addons {
		if a.Kind != kind {
			continue
		}
		k, ok := addons.Get(kind)
		if !ok {
			return nil, domain.ErrNotFound
		}
		pw := ""
		if k.Password {
			if pw, err = s.RevealEnv(ctx, actor, botID, domain.AddonPasswordVar(kind)); err != nil {
				return nil, err
			}
		}
		return k.BotEnv(pw), nil
	}
	return nil, domain.ErrNotFound
}

// AddonLogs returns the last lines an add-on wrote.
func (s *BotService) AddonLogs(ctx context.Context, actor domain.User, botID, kind string, lines int) (string, error) {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermViewConsole, false)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(b.Addons, func(a domain.BotAddon) bool { return a.Kind == kind }) {
		return "", domain.ErrNotFound
	}
	lines = min(max(lines, 1), 500)
	if s.remote(b.NodeID) {
		// Never the panel's own containers: the add-on runs on the node.
		if s.RemoteAddons == nil {
			return "", domain.Invalid("add-on logs are not available for servers on remote nodes on this panel")
		}
		return s.RemoteAddons.AddonLogs(ctx, b.NodeID, b.ID, kind, lines)
	}
	if s.AddonRuntime == nil {
		return "", domain.ErrRunnerUnavailable
	}
	return s.AddonRuntime.AddonLogs(ctx, b.ID, kind, lines)
}
