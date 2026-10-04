package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/diag"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/legacy"
)

// legacyChecks logs one WARNING per trace of a BotPanel 0.4.0 installation
// and returns them as diagnostics. dk may be nil (local runner off). Nothing
// legacy is read beyond its existence, and nothing is changed.
func legacyChecks(ctx context.Context, log *slog.Logger, cfg config.Config, dk *docker.Adapter) []diag.Check {
	findings := legacy.Detect(os.Environ(), cfg.DBPath)
	if dk != nil {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		n, err := dk.CountLabeled(cctx, legacy.ContainerLabel)
		cancel()
		if err == nil && n > 0 {
			findings = append(findings, legacy.Finding{What: fmt.Sprintf("%d running container(s) of the former panel (label %s) are still on this Docker host; "+
				"a bot running there and here with the same Discord token will fight over the gateway session", n, legacy.ContainerLabel)})
		}
	}
	var out []diag.Check
	for i, f := range findings {
		log.Warn("WARNING: a "+legacy.Release+" installation was found: "+f.What+". "+legacy.Explanation, "docs", legacy.DocsPath)
		out = append(out, diag.Check{ID: fmt.Sprintf("legacy-%d", i+1), Group: "Panel", Title: "Former installation", Status: diag.Warn,
			Detail: strings.ToUpper(f.What[:1]) + f.What[1:] + ". " + legacy.Release + " data is not imported or changed.",
			Fix:    "Follow " + legacy.DocsPath + " to stop and remove the former panel, its containers and data."})
	}
	return out
}

// listenCheck warns when the panel listens beyond loopback in production
// while its public address is plain http on a non-local host: sessions and
// passwords would then cross the network unencrypted.
func listenCheck(log *slog.Logger, cfg config.Config) []diag.Check {
	if !cfg.Production || !strings.HasPrefix(cfg.PublicURL, "http://") || loopbackListen(cfg.Listen) {
		return nil
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || localHostName(u.Hostname()) {
		return nil
	}
	log.Warn("WARNING: the panel listens on a non-loopback address and RIVET_PUBLIC_URL is plain http; "+
		"passwords and session cookies can cross the network unencrypted. Put a TLS reverse proxy in front and use an https public URL",
		"listen", cfg.Listen, "public_url", cfg.PublicURL)
	return []diag.Check{{ID: "listen_tls", Group: "Panel", Title: "Unencrypted public address", Status: diag.Warn,
		Detail: fmt.Sprintf("Listening on %s with the public address %s.", cfg.Listen, cfg.PublicURL),
		Fix:    "Serve the panel through an HTTPS reverse proxy and set RIVET_PUBLIC_URL to its https:// origin, or listen on 127.0.0.1."}}
}

func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return localHostName(host)
}

func localHostName(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// hostPathCheck makes bind mounts work when the panel itself runs in a
// container: the Docker daemon resolves bind sources on the host, so the
// paths of workspaces, add-on data and scratch directories are translated
// through the panel container's own volume or bind mounts. A data directory
// that is on no mount cannot work at all and stops the start with an
// explanation.
func hostPathCheck(ctx context.Context, log *slog.Logger, cfg config.Config, dk *docker.Adapter) ([]diag.Check, error) {
	state := filepath.Dir(cfg.DBPath)
	need := []string{cfg.DataRoot, filepath.Join(state, "addons"), filepath.Join(state, "ai-scratch")}
	m, err := dk.DetectHostPaths(ctx, need...)
	switch {
	case errors.Is(err, docker.ErrSelfUnknown):
		log.Warn("the panel runs in a container that the Docker daemon cannot identify; bind mounts assume that "+
			cfg.DataRoot+" has the same path on the Docker host. Mount the data directory at the same absolute path inside and outside the container",
			"data_root", cfg.DataRoot)
		return []diag.Check{{ID: "host_paths", Group: "Host", Title: "Container data paths", Status: diag.Warn,
			Detail: "The panel's own container could not be identified, so bot workspaces are mounted by their path inside the panel container.",
			Fix:    "Bind-mount the data directory at the same absolute path on the host (for example -v /var/lib/rivetpanel:/var/lib/rivetpanel)."}}, nil
	case err != nil:
		return nil, fmt.Errorf("container data paths: %w", err)
	case len(m) == 0:
		return nil, nil
	}
	dk.SetPathMap(m)
	detail := "Bot workspaces are mounted from " + m.Translate(cfg.DataRoot) + " on the Docker host."
	log.Info("panel runs in a container; bind-mount sources are translated to Docker host paths",
		"data_root", cfg.DataRoot, "host_data_root", m.Translate(cfg.DataRoot))
	return []diag.Check{{ID: "host_paths", Group: "Host", Title: "Container data paths", Status: diag.OK, Detail: detail}}, nil
}

// healthCmd asks the running panel's liveness endpoint. It is the image's
// HEALTHCHECK, so it needs neither curl nor wget.
func healthCmd() error {
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("RIVET_LISTEN %q: %w", cfg.Listen, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 5 * time.Second}
	res, err := c.Get("http://" + net.JoinHostPort(host, port) + "/api/v1/healthz")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: HTTP %d", res.StatusCode)
	}
	fmt.Println("ok")
	return nil
}
