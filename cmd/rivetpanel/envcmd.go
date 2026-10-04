package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/service"
)

// processEnv is the process environment as a config.Lookup.
func processEnv(name string) (string, bool) {
	v, ok := os.LookupEnv(name)
	return strings.TrimSpace(v), ok
}

// supervised reports whether something will start the panel again after it
// exits: systemd (it sets INVOCATION_ID for services), a container (whose
// restart policy the panel cannot see) or Kubernetes. The administration page
// only offers "Restart panel" then.
func supervised() bool {
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// effectiveConfig layers the overrides saved from the administration page over
// the environment. A set that cannot be read or no longer validates is ignored
// and reported on the page, so a bad save can never stop the panel from
// starting; the command line (rivetpanel env reset) is the way back either way.
func effectiveConfig(ctx context.Context, base config.Config, svc *service.PanelEnvService, log *slog.Logger) config.Config {
	applied, unreadable, err := svc.Overrides(ctx)
	if err != nil {
		log.Warn("saved environment overrides could not be read; starting with the environment only", "err", err)
		svc.Started(nil, "The saved overrides could not be read: "+err.Error(), nil)
		return base
	}
	if len(unreadable) > 0 {
		log.Warn("saved environment overrides cannot be decrypted with the loaded keys", "variables", unreadable)
	}
	if len(applied) == 0 {
		svc.Started(nil, "", unreadable)
		return base
	}
	cfg, err := config.LoadLookup(config.Overlay(svc.Base, applied))
	if err != nil {
		log.Error("saved environment overrides are invalid; starting with the environment only", "err", err)
		svc.Started(nil, strings.ReplaceAll(err.Error(), "\n", "; "), unreadable)
		return base
	}
	names := make([]string, 0, len(applied))
	for n := range applied {
		names = append(names, n)
	}
	sort.Strings(names)
	log.Info("environment overrides from the administration page are in effect", "variables", names)
	svc.Started(applied, "", unreadable)
	return cfg
}

// envCmd shows or drops the overrides saved from the administration page. It
// needs only the database, so it works when the panel will not start.
//
//	rivetpanel env                 list the overrides (secret values are not shown)
//	rivetpanel env reset           drop every override
//	rivetpanel env reset NAME...   drop the named overrides
func envCmd(args []string) error {
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	stored, err := db.EnvOverrides(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(stored))
	for n := range stored {
		names = append(names, n)
	}
	sort.Strings(names)

	if len(args) == 0 {
		if len(names) == 0 {
			fmt.Println("no overrides saved from the administration page")
			return nil
		}
		for _, n := range names {
			v := fmt.Sprintf("%q", stored[n].Value)
			if stored[n].Cipher != nil {
				v = "(secret)"
			}
			fmt.Printf("%-36s %s\n", n, v)
		}
		fmt.Println("They take effect when the panel starts. Drop them with: rivetpanel env reset [NAME...]")
		return nil
	}
	if args[0] != "reset" {
		return errors.New("usage: rivetpanel env [reset [NAME...]]")
	}
	drop := args[1:]
	if len(drop) == 0 {
		drop = names
	}
	for _, n := range drop {
		if _, ok := stored[n]; !ok {
			return fmt.Errorf("%s has no saved override", n)
		}
	}
	if len(drop) == 0 {
		fmt.Println("nothing to reset")
		return nil
	}
	if err := db.PutEnvOverrides(ctx, nil, drop, "cli", time.Now().UnixMilli()); err != nil {
		return err
	}
	fmt.Printf("dropped %d override(s): %s\nRestart the panel for them to stop applying.\n", len(drop), strings.Join(drop, ", "))
	return nil
}
