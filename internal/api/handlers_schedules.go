package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type scheduleDTO struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	Spec        string    `json:"spec"`
	Timezone    string    `json:"timezone"`
	Enabled     bool      `json:"enabled"`
	OwnerEmail  string    `json:"owner_email"`
	NextRunMS   *int64    `json:"next_run_at_ms"`
	LastRunMS   *int64    `json:"last_run_at_ms"`
	LastStatus  *string   `json:"last_status"`
	LastMessage *string   `json:"last_message"`
	Upcoming    []int64   `json:"upcoming"`
	CanEdit     bool      `json:"can_edit"`
	CreatedAtMS int64     `json:"created_at_ms"`
	Tasks       []taskDTO `json:"tasks"`
}

type taskDTO struct {
	Action            string `json:"action"`
	Payload           string `json:"payload"`
	DelaySeconds      int    `json:"delay_seconds"`
	ContinueOnFailure bool   `json:"continue_on_failure"`
}

func toSchedule(v service.ScheduleView) scheduleDTO {
	up := v.Upcoming
	if up == nil {
		up = []int64{}
	}
	next := v.NextRunMS
	if !v.Enabled {
		next = nil
	}
	tasks := make([]taskDTO, len(v.Tasks))
	for i, t := range v.Tasks {
		tasks[i] = taskDTO{t.Action, t.Payload, t.DelaySeconds, t.ContinueOnFailure}
	}
	return scheduleDTO{v.ID, v.Action, v.Spec, v.Timezone, v.Enabled, v.OwnerEmail, next, v.LastRunMS, v.LastStatus,
		v.LastMessage, up, v.CanEdit, v.CreatedAtMS, tasks}
}

type scheduleBody struct {
	Action   *string    `json:"action"`
	Spec     *string    `json:"spec"`
	Timezone *string    `json:"timezone"`
	Enabled  *bool      `json:"enabled"`
	Tasks    *[]taskDTO `json:"tasks"`
}

func (b scheduleBody) input() service.ScheduleInput {
	in := service.ScheduleInput{Action: b.Action, Spec: b.Spec, Timezone: b.Timezone, Enabled: b.Enabled}
	if b.Tasks != nil {
		tasks := make([]domain.ScheduleTask, len(*b.Tasks))
		for i, t := range *b.Tasks {
			tasks[i] = domain.ScheduleTask{Action: t.Action, Payload: t.Payload, DelaySeconds: t.DelaySeconds, ContinueOnFailure: t.ContinueOnFailure}
		}
		in.Tasks = &tasks
	}
	return in
}

func (s *panel) listSchedules(c fiber.Ctx) error {
	vs, err := s.schedules.List(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	out := make([]scheduleDTO, len(vs))
	for i, v := range vs {
		out[i] = toSchedule(v)
	}
	return c.JSON(fiber.Map{"schedules": out})
}

func (s *panel) createSchedule(c fiber.Ctx) error {
	var in scheduleBody
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.schedules.Create(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.input())
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(toSchedule(v))
}

func (s *panel) patchSchedule(c fiber.Ctx) error {
	var in scheduleBody
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.schedules.Update(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("sid")), in.input())
	if err != nil {
		return err
	}
	return c.JSON(toSchedule(v))
}

func (s *panel) deleteSchedule(c fiber.Ctx) error {
	if err := s.schedules.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("sid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) runSchedule(c fiber.Ctx) error {
	status, msg, err := s.schedules.RunNow(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("sid")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"status": status, "message": msg})
}

func (s *panel) previewSchedule(c fiber.Ctx) error {
	times, err := s.schedules.Preview(c.Query("spec"), c.Query("timezone"), 5)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"upcoming": times})
}
