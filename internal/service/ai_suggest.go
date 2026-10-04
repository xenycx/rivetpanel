package service

import (
	"context"
	"errors"
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

// Suggest runs one completion without tools on the default provider and
// returns the reply and the model used. It is for structured suggestions
// (such as a deployment plan) that the person reviews before anything
// changes; it never acts on its own.
func (s *AIService) Suggest(ctx context.Context, actor domain.User, system, user string) (string, string, error) {
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
	cfg.Temperature = 0.1
	msgs := []operator.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	var resp operator.Response
	for attempt := 0; attempt < 2; attempt++ {
		resp, err = s.Provider.Complete(ctx, cfg, msgs, nil, nil)
		var pe *operator.ProviderError
		if err == nil || !errors.As(err, &pe) || !pe.Temporary() {
			break
		}
	}
	if err != nil {
		_, msg := operator.FriendlyError(err)
		return "", cfg.Model, errors.New(msg)
	}
	if s.Log != nil {
		s.Log.Info("AI suggestion", "user", actor.ID, "provider", p.Name, "model", cfg.Model,
			"prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens)
	}
	return resp.Content, cfg.Model, nil
}
