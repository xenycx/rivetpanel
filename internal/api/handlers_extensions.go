package api

import (
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/extensions/bundle"
	extrt "github.com/xenycx/rivetpanel/internal/extensions/runtime"
	"github.com/xenycx/rivetpanel/internal/service"
)

// extensionPublicRoutes registers signed requests from outside (Discord
// interactions). It must run before the authenticated group is created, so
// session middleware never applies; the signature is checked before any
// extension code runs.
func (s *panel) extensionPublicRoutes(v1 fiber.Router) {
	if s.extensions == nil {
		return
	}
	publicLimit := newLimiter(limiter.Config{Max: 120, Expiration: time.Minute, KeyGenerator: func(c fiber.Ctx) string { return c.IP() }})
	v1.Post("/ext-public/:ext/*", publicLimit, s.extPublicRoute)
}

// extensionRoutes registers the extension API. With the extensions module
// off the service is nil and none of these routes exist.
func (s *panel) extensionRoutes(authed fiber.Router) {
	if s.extensions == nil {
		return
	}
	authed.Get("/admin/extensions", s.listExtensions)
	authed.Post("/admin/extensions/upload", s.uploadExtension)
	authed.Get("/admin/extensions/:ext", s.getExtension)
	authed.Post("/admin/extensions/:ext/activate", s.activateExtension)
	authed.Post("/admin/extensions/:ext/enable", s.enableExtension(true))
	authed.Post("/admin/extensions/:ext/disable", s.enableExtension(false))
	authed.Put("/admin/extensions/:ext/settings", s.configureExtension)
	authed.Post("/admin/extensions/:ext/jobs/:job/run", s.runExtensionJob)
	authed.Delete("/admin/extensions/:ext/versions/:version", s.deleteExtensionVersion)
	authed.Delete("/admin/extensions/:ext", s.uninstallExtension)
	authed.Get("/admin/extension-publishers", s.listExtensionPublishers)
	authed.Post("/admin/extension-publishers", s.addExtensionPublisher)
	authed.Delete("/admin/extension-publishers/:key", s.revokeExtensionPublisher)

	authed.Get("/extensions/contributions", s.extensionContributions)
	authed.Put("/extensions/:ext/consent", s.setExtensionConsent)
	authed.Get("/extensions/confirmations/:token", s.getExtensionConfirmation)
	authed.Post("/extensions/confirmations/:token", s.confirmExtensionWrite)
	authed.Get("/ext-ui/:ext/:page", s.extensionUIPage)
	authed.Get("/bots/:id/extensions", s.botExtensions)
	authed.Put("/bots/:id/extensions/:ext", s.setBotExtension)
	for _, m := range []string{fiber.MethodGet, fiber.MethodPost, fiber.MethodPut, fiber.MethodDelete} {
		authed.Add([]string{m}, "/ext/:ext/*", s.extUserRoute)
		authed.Add([]string{m}, "/bots/:id/ext/:ext/*", s.extBotRoute)
	}
}

func (s *panel) listExtensions(c fiber.Ctx) error {
	list, err := s.extensions.Catalog(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"extensions": list, "capabilities": bundle.Capabilities, "safe_mode": s.extensions.SafeMode,
		"unsigned_allowed": s.extensions.AllowUnsigned, "engine": extrt.Mode()})
}

func (s *panel) getExtension(c fiber.Ctx) error {
	v, err := s.extensions.Get(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), strings.Clone(c.Query("version")))
	if err != nil {
		return err
	}
	return c.JSON(v)
}

func (s *panel) uploadExtension(c fiber.Ctx) error {
	if n := c.RequestCtx().Request.Header.ContentLength(); n < 0 || n > bundle.MaxCompressed {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "extension bundles are at most 16 MiB")
	}
	raw := c.Body()
	if len(raw) > bundle.MaxCompressed {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "extension bundles are at most 16 MiB")
	}
	v, err := s.extensions.Upload(c.Context(), currentUser(c), append([]byte(nil), raw...))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, v.ID+" "+v.Version)
	return c.Status(fiber.StatusCreated).JSON(v)
}

func (s *panel) activateExtension(c fiber.Ctx) error {
	var in struct {
		Version string   `json:"version"`
		Grants  []string `json:"grants"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return domain.Invalid("malformed JSON")
	}
	id := strings.Clone(c.Params("ext"))
	c.Locals(keyAuditTarget, clip(id+" "+in.Version+" grants="+strings.Join(in.Grants, ","), 300))
	v, err := s.extensions.Activate(c.Context(), currentUser(c), id, in.Version, in.Grants)
	if err != nil {
		return err
	}
	return c.JSON(v)
}

func (s *panel) enableExtension(on bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		v, err := s.extensions.SetEnabled(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), on)
		if err != nil {
			return err
		}
		return c.JSON(v)
	}
}

func (s *panel) configureExtension(c fiber.Ctx) error {
	var in struct {
		Values map[string]any `json:"values"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return domain.Invalid("malformed JSON")
	}
	id := strings.Clone(c.Params("ext"))
	keys := make([]string, 0, len(in.Values))
	for k := range in.Values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	c.Locals(keyAuditTarget, clip(id+" settings="+strings.Join(keys, ","), 300)) // names only, never values
	v, err := s.extensions.Configure(c.Context(), currentUser(c), id, in.Values)
	if err != nil {
		return err
	}
	return c.JSON(v)
}

func (s *panel) runExtensionJob(c fiber.Ctx) error {
	if err := s.extensions.RunJobNow(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), strings.Clone(c.Params("job"))); err != nil {
		return err
	}
	v, err := s.extensions.Get(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), "")
	if err != nil {
		return err
	}
	return c.JSON(v)
}

func (s *panel) deleteExtensionVersion(c fiber.Ctx) error {
	if err := s.extensions.DeleteVersion(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), strings.Clone(c.Params("version"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) uninstallExtension(c fiber.Ctx) error {
	if err := s.extensions.Uninstall(c.Context(), currentUser(c), strings.Clone(c.Params("ext"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) listExtensionPublishers(c fiber.Ctx) error {
	ps, err := s.extensions.Publishers(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"publishers": ps})
}

func (s *panel) addExtensionPublisher(c fiber.Ctx) error {
	var in struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return domain.Invalid("malformed JSON")
	}
	p, err := s.extensions.AddPublisher(c.Context(), currentUser(c), in.Name, in.PublicKey)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(p)
}

func (s *panel) revokeExtensionPublisher(c fiber.Ctx) error {
	if err := s.extensions.RevokePublisher(c.Context(), currentUser(c), strings.Clone(c.Params("key"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) extensionContributions(c fiber.Ctx) error {
	cs, err := s.extensions.Contributions(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"extensions": cs})
}

func (s *panel) setExtensionConsent(c fiber.Ctx) error {
	var in struct {
		Scopes []string `json:"scopes"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return domain.Invalid("malformed JSON")
	}
	c.Locals(keyAuditTarget, clip(strings.Clone(c.Params("ext"))+" scopes="+strings.Join(in.Scopes, ","), 300))
	if err := s.extensions.SetConsent(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), in.Scopes); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) botExtensions(c fiber.Ctx) error {
	list, err := s.extensions.BotExtensions(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"extensions": list})
}

func (s *panel) setBotExtension(c fiber.Ctx) error {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.Bind().JSON(&in); err != nil {
		return domain.Invalid("malformed JSON")
	}
	if err := s.extensions.SetBotEnabled(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), strings.Clone(c.Params("id")), in.Enabled); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) getExtensionConfirmation(c fiber.Ctx) error {
	p, err := s.extensions.PendingAddonWrite(currentUser(c), strings.Clone(c.Params("token")))
	if err != nil {
		return err
	}
	return c.JSON(p)
}

// confirmExtensionWrite is reached only from the panel's own dialog: it needs
// the session cookie and CSRF token, which a sandboxed extension page lacks.
func (s *panel) confirmExtensionWrite(c fiber.Ctx) error {
	res, err := s.extensions.ConfirmAddonWrite(c.Context(), currentUser(c), strings.Clone(c.Params("token")))
	if err != nil {
		return err
	}
	return c.JSON(res)
}

// extensionUIPage serves a custom extension page. It runs sandboxed with an
// opaque origin: no cookies, no CSRF token, no access to the panel's pages,
// no network; it talks to the panel only through postMessage.
func (s *panel) extensionUIPage(c fiber.Ctx) error {
	html, hashes, err := s.extensions.UIPage(c.Context(), currentUser(c), strings.Clone(c.Params("ext")), strings.Clone(c.Params("page")))
	if err != nil {
		return err
	}
	script := "'none'"
	if len(hashes) > 0 {
		script = strings.Join(hashes, " ")
	}
	c.Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src "+script+
		"; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	c.Set("X-Frame-Options", "SAMEORIGIN")
	c.Set(fiber.HeaderCacheControl, "private, no-cache")
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Send(html)
}

func (s *panel) extUserRoute(c fiber.Ctx) error {
	u := currentUser(c)
	return s.serveExtRoute(c, &u, "user", "")
}

func (s *panel) extBotRoute(c fiber.Ctx) error {
	u := currentUser(c)
	return s.serveExtRoute(c, &u, "bot", strings.Clone(c.Params("id")))
}

func (s *panel) extPublicRoute(c fiber.Ctx) error {
	if n := c.RequestCtx().Request.Header.ContentLength(); n > 64<<10 {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body too large")
	}
	return s.serveExtRoute(c, nil, "public", "")
}

// passHeaders are the request headers an extension sees.
var passHeaders = []string{"content-type", "x-signature-ed25519", "x-signature-timestamp", "x-signature", "user-agent"}

func (s *panel) serveExtRoute(c fiber.Ctx, u *domain.User, auth, botID string) error {
	headers := map[string]string{}
	for _, h := range passHeaders {
		if v := c.Get(h); v != "" {
			headers[h] = strings.Clone(v)
		}
	}
	query := map[string]string{}
	for k, v := range c.Queries() {
		if len(query) < 32 {
			query[strings.Clone(k)] = strings.Clone(v)
		}
	}
	res, err := s.extensions.HandleRoute(c.Context(), u, strings.Clone(c.Params("ext")), service.ExtensionRouteRequest{
		Auth: auth, BotID: botID, Method: c.Method(), Path: "/" + strings.Clone(c.Params("*")),
		Query: query, Headers: headers, Body: append([]byte(nil), c.Body()...),
	})
	if err != nil {
		return err
	}
	c.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	c.Set(fiber.HeaderCacheControl, "no-store")
	if res.ContentType != "" {
		c.Set(fiber.HeaderContentType, res.ContentType)
	}
	c.Status(res.Status)
	if len(res.Body) == 0 {
		return nil
	}
	return c.Send(res.Body)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
