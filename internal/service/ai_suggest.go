package service

import (
	"context"
	"errors"
	"strings"
	"time"

	operator "github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// ErrNoAIProvider means no enabled AI provider is configured.
var ErrNoAIProvider = errors.New("no AI provider is enabled")

// defaultProvider returns the enabled default provider (or the first enabled).
func (s *AIService) defaultProvider(ctx context.Context) (domain.AIProviderProfile, error) {
	ps, err := s.Store.ListAIProviders(ctx)
	if err != nil {
		return domain.AIProviderProfile{}, err
	}
	var pick *domain.AIProviderProfile
	for i := range ps {
		if ps[i].Enabled && ps[i].KeyID != nil && (pick == nil || ps[i].Default) {
			pick = &ps[i]
		}
	}
	if pick == nil {
		return domain.AIProviderProfile{}, ErrNoAIProvider
	}
	return *pick, nil
}

// AIAvailable reports whether one-shot suggestions can run.
func (s *AIService) AIAvailable(ctx context.Context) bool {
	if s == nil {
		return false
	}
	_, err := s.defaultProvider(ctx)
	return err == nil
}

// suggestMinTokens is the output room a structured suggestion always gets.
// A provider's own cap is often sized for chat replies (4096); a reasoning
// model spends part of the budget thinking, and a cut-off reply carries no
// usable JSON at all.
const suggestMinTokens = 16384

// Suggest runs a completion without tools on the default provider and
// returns the reply that parse accepted, and the model used. It is for
// structured suggestions (such as a deployment plan) that the person reviews
// before anything changes; it never acts on its own.
//
// The reply is requested as a JSON object where the provider supports that.
// A reply that was cut off, empty or that parse rejects is asked for once
// more, with the reason, before Suggest gives up.
func (s *AIService) Suggest(ctx context.Context, actor domain.User, system, user string, parse func(reply string) error) (string, string, error) {
	s.init()
	p, err := s.defaultProvider(ctx)
	if err != nil {
		return "", "", err
	}
	_, cfg, err := s.providerConfig(ctx, p.ID)
	if err != nil {
		return "", "", err
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 3*time.Minute {
		cfg.Timeout = 3 * time.Minute
	}
	if cfg.MaxTokens > 0 && cfg.MaxTokens < suggestMinTokens {
		cfg.MaxTokens = suggestMinTokens
	}
	cfg.Temperature = 0.1
	cfg.JSONObject = true
	msgs := []operator.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	var lastErr error
	for round := 0; round < 2; round++ {
		resp, err := s.complete(ctx, &cfg, msgs)
		if err != nil {
			_, msg := operator.FriendlyError(err)
			return "", cfg.Model, errors.New(msg)
		}
		if s.Log != nil {
			s.Log.Info("AI suggestion", "user", actor.ID, "provider", p.Name, "model", cfg.Model, "round", round+1,
				"finish", resp.FinishReason, "prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens)
		}
		reply := stripThinking(resp.Content)
		switch {
		case resp.FinishReason == "length":
			lastErr = errors.New("the AI reply was cut off before the plan was complete")
		case strings.TrimSpace(reply) == "":
			lastErr = errors.New("the AI returned an empty reply")
		default:
			if lastErr = parse(reply); lastErr == nil {
				return reply, cfg.Model, nil
			}
		}
		msgs = append(msgs,
			operator.Message{Role: "assistant", Content: clip(reply, 4000)},
			operator.Message{Role: "user", Content: "That reply could not be used (" + lastErr.Error() + "). Answer again with only the complete JSON object in the requested shape, without reasoning or prose, keeping descriptions short."})
	}
	return "", cfg.Model, lastErr
}

// complete runs one request, retrying temporary provider failures once and
// dropping the JSON-object request if the provider rejects it.
func (s *AIService) complete(ctx context.Context, cfg *operator.Config, msgs []operator.Message) (operator.Response, error) {
	var resp operator.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = s.Provider.Complete(ctx, *cfg, msgs, nil, nil)
		var pe *operator.ProviderError
		if err == nil || !errors.As(err, &pe) {
			break
		}
		if pe.Status == 400 || pe.Status == 422 {
			if !cfg.JSONObject {
				break
			}
			cfg.JSONObject = false // not supported here; the prompt still asks for JSON
			continue
		}
		if !pe.Temporary() || attempt > 0 {
			break
		}
	}
	return resp, err
}

// stripThinking removes reasoning that some models put in the reply itself
// (<think>…</think>), so braces in it are never mistaken for the answer.
func stripThinking(s string) string {
	for {
		i := strings.Index(s, "<think>")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "</think>")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+len("</think>"):]
	}
}
