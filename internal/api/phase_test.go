package api

import (
	"errors"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestPhaseOf(t *testing.T) {
	r := func(s string) *string { return &s }
	cases := []struct {
		name   string
		b      domain.Bot
		runner error
		noRun  bool
		want   string
	}{
		{"stopped", domain.Bot{DesiredState: "stopped", ObservedState: "stopped"}, nil, false, "stopped"},
		{"stopped after failure", domain.Bot{DesiredState: "stopped", ObservedState: "failed"}, nil, false, "stopped"},
		{"stopping", domain.Bot{DesiredState: "stopped", ObservedState: "running", Generation: 2, ObservedGeneration: 1}, nil, false, "stopping"},
		{"queued start", domain.Bot{DesiredState: "running", ObservedState: "stopped", Generation: 2, ObservedGeneration: 1}, nil, false, "queued"},
		{"building", domain.Bot{DesiredState: "running", ObservedState: "building", Generation: 2, ObservedGeneration: 1}, nil, false, "building"},
		{"running", domain.Bot{DesiredState: "running", ObservedState: "running", Generation: 2, ObservedGeneration: 2}, nil, false, "running"},
		{"restart pending", domain.Bot{DesiredState: "running", ObservedState: "running", Generation: 3, ObservedGeneration: 2}, nil, false, "restarting"},
		{"crash backoff", domain.Bot{DesiredState: "running", ObservedState: "failed", Generation: 2, ObservedGeneration: 2, StateReason: r(domain.ReasonCrashBackoff)}, nil, false, "retrying"},
		{"setup retry", domain.Bot{DesiredState: "running", ObservedState: "failed", Generation: 2, ObservedGeneration: 2, StateReason: r(domain.ReasonBuildFailed)}, nil, false, "retrying"},
		{"gave up", domain.Bot{DesiredState: "running", ObservedState: "failed", Generation: 2, ObservedGeneration: 2, StateReason: r(domain.ReasonGaveUp)}, nil, false, "failed"},
		{"never policy", domain.Bot{DesiredState: "running", ObservedState: "failed", Generation: 2, ObservedGeneration: 2, StateReason: r(domain.ReasonExitedNoRetry)}, nil, false, "failed"},
		{"clean exit", domain.Bot{DesiredState: "running", ObservedState: "stopped", Generation: 2, ObservedGeneration: 2, StateReason: r(domain.ReasonCleanExit)}, nil, false, "exited"},
		{"unknown", domain.Bot{DesiredState: "running", ObservedState: "unknown", Generation: 2, ObservedGeneration: 2}, nil, false, "checking"},
		{"runner offline", domain.Bot{DesiredState: "running", ObservedState: "unknown"}, errors.New("down"), false, "runner_offline"},
		{"no runner", domain.Bot{DesiredState: "running", ObservedState: "stopped"}, nil, true, "no_runner"},
		{"running despite runner blip", domain.Bot{DesiredState: "running", ObservedState: "running"}, errors.New("down"), false, "running"},
		{"deleting", domain.Bot{DesiredState: "deleted", ObservedState: "running"}, nil, false, "deleting"},
	}
	for _, c := range cases {
		if got := phaseOf(c.b, c.runner, !c.noRun); got != c.want {
			t.Errorf("%s: phase = %s, want %s", c.name, got, c.want)
		}
	}
}
