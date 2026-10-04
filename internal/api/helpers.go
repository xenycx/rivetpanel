package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type ctxKey int

const (
	keyUser ctxKey = iota
	keyToken
)

// decode parses a strict JSON body: known fields only, exactly one value.
func decode(c fiber.Ctx, dst any) error {
	if !strings.HasPrefix(strings.ToLower(c.Get(fiber.HeaderContentType)), "application/json") {
		return fiber.NewError(fiber.StatusUnsupportedMediaType, "content type must be application/json")
	}
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if _, err := dec.Token(); err != io.EOF {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	return nil
}

func currentUser(c fiber.Ctx) domain.User { return fiber.Locals[domain.User](c, keyUser) }
func currentToken(c fiber.Ctx) string     { return fiber.Locals[string](c, keyToken) }

type userDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	CreatedAtMS int64  `json:"created_at_ms"`
	// RoleID is the custom role ("" = the built-in role in Role).
	RoleID        string `json:"role_id"`
	RoleName      string `json:"role_name"`
	EmailVerified bool   `json:"email_verified"`
}

func toUser(u domain.User) userDTO {
	avatar := ""
	if len(u.AvatarJPEG) > 0 {
		avatar = "/api/v1/users/" + u.ID + "/avatar?v=" + strconv.FormatInt(u.UpdatedAtMS, 10)
	}
	return userDTO{u.ID, u.Email, u.DisplayName, avatar, u.Role, u.Disabled, u.CreatedAtMS, u.RoleID, u.RoleName, u.EmailVerified}
}

type botDTO struct {
	ID                 string   `json:"id"`
	OwnerID            string   `json:"owner_id"`
	WorkspaceID        string   `json:"workspace_id"`
	NodeID             string   `json:"node_id"`
	Name               string   `json:"name"`
	Runtime            string   `json:"runtime"`
	ImageRef           string   `json:"image_ref"`
	Argv               []string `json:"argv"`
	MemoryBytes        int64    `json:"memory_bytes"`
	NanoCPUs           int64    `json:"nano_cpus"`
	PidsLimit          int64    `json:"pids_limit"`
	DesiredState       string   `json:"desired_state"`
	ObservedState      string   `json:"observed_state"`
	Generation         int64    `json:"generation"`
	ObservedGeneration int64    `json:"observed_generation"`
	LastExitCode       *int64   `json:"last_exit_code"`
	LastError          *string  `json:"last_error"`
	CreatedAtMS        int64    `json:"created_at_ms"`
	UpdatedAtMS        int64    `json:"updated_at_ms"`
	DiscordUserID      string   `json:"discord_user_id"`
	DiscordUsername    string   `json:"discord_username"`
	DiscordAvatarURL   string   `json:"discord_avatar_url"`
	// Permissions is the caller's domain.Perm* mask on this bot; Shared is true
	// when the caller is a sub-user rather than the owner or an administrator.
	Permissions int  `json:"permissions"`
	Shared      bool `json:"shared"`

	Entrypoint     []string  `json:"entrypoint"`
	SourceType     string    `json:"source_type"`
	TemplateID     *string   `json:"template_id"`
	NetworkEnabled bool      `json:"network_enabled"`
	BandwidthKbps  *int64    `json:"bandwidth_kbps"` // recorded intent; not enforced (see docs)
	Ports          []portDTO `json:"ports"`
	AutoBackup     bool      `json:"auto_backup"`
	BuildCommand   string    `json:"build_command"`
	// LogoURL is the custom logo, else the Discord avatar ("" = neither).
	LogoURL    string   `json:"logo_url"`
	CustomLogo bool     `json:"custom_logo"`
	Addons     []string `json:"addons"` // attached add-on kinds (bot detail only)

	RestartPolicy           string `json:"restart_policy"`
	RestartMaxAttempts      int64  `json:"restart_max_attempts"`
	RestartBackoffInitialMS int64  `json:"restart_backoff_initial_ms"`
	RestartBackoffMaxMS     int64  `json:"restart_backoff_max_ms"`

	// Phase is the single lifecycle word clients display (see phaseOf);
	// StateReason, RestartCount and NextRetryAtMS explain it.
	Phase         string  `json:"phase"`
	StateReason   *string `json:"state_reason"`
	RestartCount  int64   `json:"restart_count"`
	NextRetryAtMS *int64  `json:"next_retry_at_ms"`
	// LastStartedAtMS is when it last became running; nil = never ran.
	LastStartedAtMS *int64 `json:"last_started_at_ms"`

	Tags     []string `json:"tags"`     // shared labels
	Favorite bool     `json:"favorite"` // the caller's star

	// Kind is "bot" or "game". Game servers also carry their blueprint,
	// image choice ("" = automatic), installation state and allocations.
	Kind              string     `json:"kind"`
	BlueprintID       string     `json:"blueprint_id,omitempty"`
	BlueprintRevision int64      `json:"blueprint_revision,omitempty"`
	ImageChoice       string     `json:"image_choice,omitempty"`
	InstallState      string     `json:"install_state,omitempty"`
	InstalledVersion  string     `json:"installed_version,omitempty"` // what the last installation resolved, e.g. "1.21.11"
	Allocations       []allocDTO `json:"allocations,omitempty"`
}

type allocDTO struct {
	ID      string  `json:"id"`
	NodeID  string  `json:"node_id"`
	IP      string  `json:"ip"`
	Port    int     `json:"port"`
	Alias   string  `json:"alias"`
	Notes   string  `json:"notes"`
	BotID   *string `json:"bot_id"`
	Primary bool    `json:"primary"`
}

func toAlloc(a domain.Allocation) allocDTO {
	return allocDTO{ID: a.ID, NodeID: a.NodeID, IP: a.IP, Port: a.Port, Alias: a.Alias, Notes: a.Notes, BotID: a.BotID, Primary: a.Primary}
}

// phaseOf reduces intent, observation and the runner's explanation to one
// lifecycle word, so no client has to infer state from error text:
//
//	deleting checking no_runner runner_offline queued building installing starting running
//	restarting retrying failed exited stopping stopped
func phaseOf(b domain.Bot, runnerErr error, hasRunner bool) string {
	reason := ""
	if b.StateReason != nil {
		reason = *b.StateReason
	}
	pending := b.ObservedGeneration < b.Generation
	switch {
	case b.DesiredState == domain.DesiredDeleted:
		return "deleting"
	case b.DesiredState == domain.DesiredRunning && b.ObservedState != "running" && !hasRunner:
		return "no_runner"
	case b.DesiredState == domain.DesiredRunning && b.ObservedState != "running" && runnerErr != nil:
		return "runner_offline"
	case b.ObservedState == "unknown":
		return "checking"
	case b.DesiredState == domain.DesiredStopped:
		if b.ObservedState == "stopped" || b.ObservedState == "failed" {
			return "stopped"
		}
		return "stopping"
	}
	// Wanted running.
	switch b.ObservedState {
	case "building":
		if b.IsGame() {
			return "installing"
		}
		return "building"
	case "starting":
		return "starting"
	case "stopping":
		return "restarting"
	case "running":
		if pending {
			return "restarting"
		}
		return "running"
	}
	if pending {
		return "queued" // intent recorded; the runner has not picked it up yet
	}
	switch reason {
	case domain.ReasonCleanExit:
		return "exited"
	case domain.ReasonGaveUp, domain.ReasonExitedNoRetry, domain.ReasonRuntimeMissing, domain.ReasonPortConflict:
		return "failed"
	}
	if b.ObservedState == "failed" {
		return "retrying"
	}
	return "exited"
}

type portDTO struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip"`
}

func toBot(b domain.Bot) botDTO {
	d := toBotBase(b)
	d.Entrypoint = b.Entrypoint
	if d.Entrypoint == nil {
		d.Entrypoint = []string{}
	}
	d.SourceType, d.TemplateID = b.SourceType, b.TemplateID
	if d.SourceType == "" {
		d.SourceType = "manual"
	}
	d.NetworkEnabled, d.BandwidthKbps = !b.NetworkDisabled, b.BandwidthKbps
	d.AutoBackup = !b.AutoBackupOff
	d.BuildCommand = b.BuildCommand
	d.Addons = make([]string, len(b.Addons))
	for i, a := range b.Addons {
		d.Addons[i] = a.Kind
	}
	d.Ports = make([]portDTO, len(b.Ports))
	for i, p := range b.Ports {
		d.Ports[i] = portDTO{p.ContainerPort, p.HostPort, p.Protocol, p.HostIP}
	}
	d.RestartPolicy, d.RestartMaxAttempts = b.RestartPolicy, b.RestartMaxAttempts
	if d.RestartPolicy == "" {
		d.RestartPolicy = domain.RestartOnFailure
	}
	d.RestartBackoffInitialMS, d.RestartBackoffMaxMS = b.RestartBackoffInitialMS, b.RestartBackoffMaxMS
	for _, a := range b.Allocations {
		d.Allocations = append(d.Allocations, toAlloc(a))
	}
	return d
}

func toBotBase(b domain.Bot) botDTO {
	return botDTO{
		ID: b.ID, OwnerID: b.OwnerID, WorkspaceID: b.WorkspaceID, NodeID: b.NodeID, Name: b.Name, Runtime: b.Runtime, ImageRef: b.ImageRef, Argv: b.Argv,
		MemoryBytes: b.MemoryBytes, NanoCPUs: b.NanoCPUs, PidsLimit: b.PidsLimit, DesiredState: b.DesiredState,
		ObservedState: b.ObservedState, Generation: b.Generation, ObservedGeneration: b.ObservedGeneration,
		LastExitCode: b.LastExitCode, LastError: b.LastError, CreatedAtMS: b.CreatedAtMS, UpdatedAtMS: b.UpdatedAtMS,
		DiscordUserID: b.DiscordUserID, DiscordUsername: b.DiscordUsername, DiscordAvatarURL: b.DiscordAvatarURL,
		Permissions: domain.PermAll,
		Phase:       phaseOf(b, nil, true), StateReason: b.StateReason, RestartCount: b.RestartCount, NextRetryAtMS: b.NextRetryAtMS,
		LastStartedAtMS: b.LastStartedAtMS, LogoURL: logoURL(b), CustomLogo: b.LogoUpdatedMS > 0,
		Kind: kindOf(b), BlueprintID: b.BlueprintID, BlueprintRevision: b.BlueprintRevision, ImageChoice: b.ImageChoice,
		InstallState: installStateOf(b), InstalledVersion: b.InstalledVersion,
	}
}

func kindOf(b domain.Bot) string {
	if b.Kind == "" {
		return domain.KindBot
	}
	return b.Kind
}

func installStateOf(b domain.Bot) string {
	if !b.IsGame() {
		return ""
	}
	return b.InstallState
}

func logoURL(b domain.Bot) string {
	if b.LogoUpdatedMS > 0 {
		return fmt.Sprintf("/api/v1/bots/%s/logo?v=%d", b.ID, b.LogoUpdatedMS)
	}
	return b.DiscordAvatarURL
}
