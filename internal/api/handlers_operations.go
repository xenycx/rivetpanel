package api

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type opDTO struct {
	ID          string          `json:"id"`
	BotID       string          `json:"bot_id"`
	BotName     string          `json:"bot_name"`
	BotKind     string          `json:"bot_kind"` // bot | game
	Kind        string          `json:"kind"`
	Trigger     string          `json:"trigger"`
	Actor       *string         `json:"actor"` // email, when a person started it
	Status      string          `json:"status"`
	Stage       string          `json:"stage"`
	SourceRef   *string         `json:"source_ref"`
	SourceLabel *string         `json:"source_label"`
	Generation  *int64          `json:"generation"`
	ResultCode  *string         `json:"result_code"`
	Message     *string         `json:"message"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	LogBytes    int64           `json:"log_bytes"`
	CreatedAtMS int64           `json:"created_at_ms"`
	StartedAtMS *int64          `json:"started_at_ms"`
	FinishedAt  *int64          `json:"finished_at_ms"`
}

func toOp(o domain.Operation) opDTO {
	d := opDTO{o.ID, o.BotID, o.BotName, o.BotKind, o.Kind, o.Trigger, o.ActorEmail, o.Status, o.Stage, o.SourceRef, o.SourceLabel,
		o.Generation, o.ResultCode, o.Message, nil, o.LogBytes, o.CreatedAtMS, o.StartedAtMS, o.FinishedAt}
	if o.DetailJSON != nil {
		d.Detail = json.RawMessage(*o.DetailJSON)
	}
	return d
}

var opKinds = map[string]bool{domain.OpBuild: true, domain.OpDeploy: true, domain.OpBackup: true, domain.OpRestore: true, domain.OpRollback: true, domain.OpPublish: true}

// opQuery parses kind (comma separated), before (ms cursor) and limit.
func opQuery(c fiber.Ctx) (kinds []string, before int64, limit int, err error) {
	if k := c.Query("kind"); k != "" {
		for _, x := range strings.Split(k, ",") {
			if !opKinds[x] {
				return nil, 0, 0, domain.Invalid("unknown operation kind")
			}
			kinds = append(kinds, strings.Clone(x))
		}
	}
	if v := c.Query("before"); v != "" {
		if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 0 {
			return nil, 0, 0, domain.Invalid("before must be a timestamp in milliseconds")
		}
	}
	limit = 50
	if v := c.Query("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 200 {
			return nil, 0, 0, domain.Invalid("limit must be between 1 and 200")
		}
	}
	return kinds, before, limit, nil
}

func opsPage(ops []domain.Operation, limit int) fiber.Map {
	out := make([]opDTO, len(ops))
	for i, o := range ops {
		out[i] = toOp(o)
	}
	m := fiber.Map{"operations": out}
	if len(ops) == limit && limit > 0 {
		m["next_before"] = ops[len(ops)-1].CreatedAtMS // cursor for the next page
	}
	return m
}

func (s *panel) listBotOperations(c fiber.Ctx) error {
	kinds, before, limit, err := opQuery(c)
	if err != nil {
		return err
	}
	ops, err := s.ops.ListForBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")), kinds, before, limit)
	if err != nil {
		return err
	}
	return c.JSON(opsPage(ops, limit))
}

func (s *panel) listActivity(c fiber.Ctx) error {
	kinds, before, limit, err := opQuery(c)
	if err != nil {
		return err
	}
	ops, err := s.ops.Activity(c.Context(), currentUser(c), c.Query("active") == "1", kinds, before, limit)
	if err != nil {
		return err
	}
	return c.JSON(opsPage(ops, limit))
}

func (s *panel) getOperation(c fiber.Ctx) error {
	op, err := s.ops.Get(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("op")))
	if err != nil {
		return err
	}
	return c.JSON(toOp(op))
}

// operationOutput returns retained output from an absolute offset. Clients
// poll with next_offset until live is false.
func (s *panel) operationOutput(c fiber.Ctx) error {
	var offset int64
	if v := c.Query("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return domain.Invalid("offset must be a non-negative number")
		}
		offset = n
	}
	ch, err := s.ops.ReadOutput(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("op")), offset, 64<<10)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	if c.Query("download") == "1" {
		c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="build-`+strings.Clone(c.Params("op"))[:8]+`.log"`)
		ch, err = s.ops.ReadOutput(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("op")), 0, 1<<30)
		if err != nil {
			return err
		}
		return c.Send(ch.Data)
	}
	return c.JSON(fiber.Map{"text": string(sanitizeUTF8(ch.Data)), "next_offset": ch.Next, "total": ch.Total,
		"truncated": ch.Truncated, "live": ch.Live})
}

// sanitizeUTF8 keeps output JSON-safe (invalid bytes become U+FFFD).
func sanitizeUTF8(b []byte) []byte { return []byte(strings.ToValidUTF8(string(b), "�")) }
