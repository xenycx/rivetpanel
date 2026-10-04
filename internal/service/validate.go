package service

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

const (
	maxArgvItems   = 32
	maxArgvItemLen = 1024
	maxEnvVars     = 128
	maxEnvNameLen  = 128
	maxEnvValueLen = 32 * 1024
	minPasswordLen = 12
	maxPasswordLen = 128
	maxPids        = 4096
)

var envNameRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Limits bound per-bot resources (administrator policy).
type Limits struct {
	MinMemoryBytes, MaxMemoryBytes int64
	MinNanoCPUs, MaxNanoCPUs       int64
	// Published-port policy: host ports must fall in [PortMin, PortMax] so bots
	// can never claim the panel, SSH or other host services. Binding beyond
	// loopback needs PortPublicBind.
	PortMin, PortMax int
	PortPublicBind   bool

	// Capacity budgets; 0 means unlimited. Per-user budgets do not apply to
	// administrators. NodeMemory caps the memory limits of all bots wanted
	// running on a node (admission, not a kernel limit).
	MaxBotsPerUser  int
	UserMemoryBytes int64
	NodeMemoryBytes int64
}

// NormalizeEmail lowercases and validates an address. SQLite's NOCASE collation
// is ASCII-only, so the canonical form is produced here.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 254 {
		return "", domain.Invalid("invalid email address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" || strings.ContainsAny(s, " ,;<>") {
		return "", domain.Invalid("invalid email address")
	}
	return s, nil
}

func validatePassword(p string) error {
	if len(p) > maxPasswordLen {
		return domain.Invalid(fmt.Sprintf("password must be at most %d bytes", maxPasswordLen))
	}
	if utf8.RuneCountInString(p) < minPasswordLen {
		return domain.Invalid(fmt.Sprintf("password must be at least %d characters", minPasswordLen))
	}
	return nil
}

func validateName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if l := utf8.RuneCountInString(n); l < 1 || l > 64 || strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", domain.Invalid("name must be 1-64 characters without control characters")
	}
	return n, nil
}

func validateArgv(rt runtimes.Runtime, argv []string) error {
	return validateStartup(rt, argv, nil)
}

// validateStartup checks the start command. Without an entrypoint, argv[0] is
// the program and must be permitted for the runtime. With one, the entrypoint's
// program must be permitted and argv are plain arguments.
func validateStartup(rt runtimes.Runtime, argv, entry []string) error {
	if len(argv) == 0 || len(argv) > maxArgvItems || len(entry) > maxArgvItems {
		return domain.Invalid(fmt.Sprintf("argv must contain 1-%d items", maxArgvItems))
	}
	for i, a := range append(append([]string(nil), entry...), argv...) {
		if len(a) > maxArgvItemLen || strings.ContainsRune(a, 0) || !utf8.ValidString(a) {
			return domain.Invalid(fmt.Sprintf("item %d is too long or contains invalid characters", i))
		}
	}
	prog := argv[0]
	if len(entry) > 0 {
		prog = entry[0]
	}
	if prog == "" || !rt.CommandAllowed(prog) {
		return domain.Invalid(fmt.Sprintf("command %q is not permitted for runtime %s", prog, rt.ID))
	}
	return nil
}

func (l Limits) validateResources(rt runtimes.Runtime, mem, cpus, pids int64) error {
	minMem := max(l.MinMemoryBytes, rt.MinMemoryBytes)
	if mem < minMem || mem > l.MaxMemoryBytes {
		return domain.Invalid(fmt.Sprintf("memory_bytes must be between %d and %d for %s", minMem, l.MaxMemoryBytes, rt.ID))
	}
	if cpus < l.MinNanoCPUs || cpus > l.MaxNanoCPUs {
		return domain.Invalid(fmt.Sprintf("nano_cpus must be between %d and %d", l.MinNanoCPUs, l.MaxNanoCPUs))
	}
	if pids < 1 || pids > maxPids {
		return domain.Invalid(fmt.Sprintf("pids_limit must be between 1 and %d", maxPids))
	}
	return nil
}

func validateEnvName(name string, system bool) error {
	if len(name) == 0 || len(name) > maxEnvNameLen || !envNameRe.MatchString(name) {
		return domain.Invalid(fmt.Sprintf("invalid environment variable name %q", name))
	}
	up := strings.ToUpper(name)
	if name == "PATH" || strings.HasPrefix(up, "LD_") || (!system && strings.HasPrefix(up, "RIVET_")) {
		return domain.Invalid(fmt.Sprintf("environment variable %q is reserved", name))
	}
	return nil
}

// MaxBuildCommand bounds a custom build command.
const MaxBuildCommand = 4096

// validateBuildCommand normalises a custom build command ("" = default).
func validateBuildCommand(cmd string) (string, error) {
	cmd = strings.TrimSpace(strings.ReplaceAll(cmd, "\r\n", "\n"))
	if len(cmd) > MaxBuildCommand || strings.ContainsRune(cmd, 0) || !utf8.ValidString(cmd) {
		return "", domain.Invalid(fmt.Sprintf("the build command must be at most %d bytes of text", MaxBuildCommand))
	}
	return cmd, nil
}

func validateEnvValue(v string) error {
	if len(v) > maxEnvValueLen || strings.ContainsRune(v, 0) {
		return domain.Invalid("environment value is too long or contains NUL")
	}
	return nil
}

func asValidation(err error, t **domain.ValidationError) bool { return errors.As(err, t) }
func asBusy(err error, t **domain.BusyError) bool             { return errors.As(err, t) }
func isNotFound(err error) bool                               { return errors.Is(err, domain.ErrNotFound) }
func isForbidden(err error) bool                              { return errors.Is(err, domain.ErrForbidden) }
func isRunnerUnavailable(err error) bool                      { return errors.Is(err, domain.ErrRunnerUnavailable) }
