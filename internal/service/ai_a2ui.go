package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/xenycx/rivetpanel/internal/a2ui"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// surfaces rebuilds the A2UI surfaces a conversation shows from its accepted
// show_ui calls, oldest first.
func (s *AIService) surfaces(ctx context.Context, conversationID string) (*a2ui.State, error) {
	runs, e := s.Store.ListAIRuns(ctx, conversationID, 50)
	if e != nil {
		return nil, e
	}
	var calls []string
	for i := len(runs) - 1; i >= 0; i-- {
		tcs, e := s.Store.ListAIToolCalls(ctx, runs[i].ID)
		if e != nil {
			return nil, e
		}
		for _, c := range tcs {
			if c.Name == "show_ui" && c.Status == "completed" {
				calls = append(calls, c.ArgumentsJSON)
			}
		}
	}
	return a2ui.Replay(calls), nil
}

// showUI checks and accepts one show_ui call. The browser draws it from the
// stored arguments once the call completes.
func (s *AIService) showUI(ctx context.Context, c domain.AIConversation, a agentArgs) (string, error) {
	raw, _ := json.Marshal(a)
	st, e := s.surfaces(ctx, c.ID)
	if e != nil {
		return "", e
	}
	res, e := st.Apply(raw)
	if e != nil {
		return "", domain.Invalid("A2UI: " + e.Error() + ". Fix it and call show_ui again.")
	}
	return res.Summary(), nil
}

// actionMessage turns a button press into the person's next message, after
// checking that this conversation really showed that button.
func (s *AIService) actionMessage(ctx context.Context, conversationID string, act a2ui.Action) (string, error) {
	st, e := s.surfaces(ctx, conversationID)
	if e != nil {
		return "", e
	}
	label, e := st.CheckAction(act)
	if e != nil {
		return "", domain.Invalid(e.Error())
	}
	act.Timestamp = time.Now().UTC().Format(time.RFC3339)
	return act.Envelope(label), nil
}
