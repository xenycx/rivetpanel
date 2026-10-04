// Package modules defines RivetPanel's stable and opt-in preview capabilities.
package modules

import (
	"fmt"
	"sort"
	"strings"
)

// State is the operator-visible maturity and availability of a module.
type State string

const (
	Off     State = "off"
	Preview State = "preview"
	Stable  State = "stable"
)

// Descriptor is safe to return to signed-in clients.
type Descriptor struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	State       State  `json:"state"`
}

// Registry is the complete module catalog keyed by stable machine name.
type Registry map[string]Descriptor

var catalog = []Descriptor{
	{Key: "core", Label: "Core hosting", Description: "Accounts, workspaces, lifecycle, files, console, backups and operations.", State: Stable},
	{Key: "applications", Label: "Applications", Description: "Language runtimes, repository deployments, telemetry and package management.", State: Stable},
	{Key: "sites", Label: "Sites", Description: "Static sites, releases and custom domains.", State: Stable},
	{Key: "ai", Label: "AI operations", Description: "Context-aware diagnostics and approval-gated repairs.", State: Stable},
	{Key: "agents", Label: "Remote nodes", Description: "Authenticated node agents, placement, drain and transfer.", State: Off},
	{Key: "game_servers", Label: "Game servers", Description: "Blueprint-based game hosting (Minecraft Java and proxies), allocations and player queries.", State: Preview},
	{Key: "identity", Label: "Extended identity", Description: "Passkeys, OIDC, LDAP, email verification and custom roles.", State: Off},
	{Key: "support", Label: "Support and content", Description: "Tickets, knowledgebase, status pages and translations.", State: Off},
	{Key: "extensions", Label: "Extensions", Description: "Signed, capability-scoped extension bundles.", State: Off},
}

// Default returns an independent copy of the built-in catalog.
func Default() Registry {
	r := make(Registry, len(catalog))
	for _, d := range catalog {
		r[d.Key] = d
	}
	return r
}

// Parse applies comma-separated key=state overrides to the built-in catalog.
// Preview modules are deliberately off until an operator opts in.
func Parse(value string) (Registry, error) {
	r := Default()
	seen := map[string]bool{}
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key, stateText, ok := strings.Cut(raw, "=")
		key, stateText = strings.TrimSpace(key), strings.TrimSpace(stateText)
		if !ok || key == "" || stateText == "" {
			return nil, fmt.Errorf("module override %q must be key=off, key=preview or key=stable", raw)
		}
		d, exists := r[key]
		if !exists {
			return nil, fmt.Errorf("unknown module %q", key)
		}
		if seen[key] {
			return nil, fmt.Errorf("module %q is configured more than once", key)
		}
		seen[key] = true
		state := State(stateText)
		if state != Off && state != Preview && state != Stable {
			return nil, fmt.Errorf("module %q has invalid state %q", key, stateText)
		}
		d.State = state
		r[key] = d
	}
	if r["core"].State != Stable {
		return nil, fmt.Errorf("core module must remain stable")
	}
	return r, nil
}

// List returns descriptors in stable key order for deterministic APIs.
func (r Registry) List() []Descriptor {
	out := make([]Descriptor, 0, len(r))
	for _, d := range r {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Enabled reports whether a module is available at any maturity level.
func (r Registry) Enabled(key string) bool {
	d, ok := r[key]
	return ok && d.State != Off
}
