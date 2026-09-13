package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// ModelThinking describes evidenced, model-specific reasoning controls.
// A nil value leaves reasoning support unknown rather than advertising every level.
type ModelThinking struct {
	Min            int      `yaml:"min"`
	Max            int      `yaml:"max"`
	ZeroAllowed    bool     `yaml:"zero_allowed"`
	DynamicAllowed bool     `yaml:"dynamic_allowed"`
	Levels         []string `yaml:"levels"`
}

// ModelProvider derives metadata from the same entries that drive routing.
type ModelProvider struct{ cfg *pluginConfig }

func NewModelProvider(cfg *pluginConfig) *ModelProvider { return &ModelProvider{cfg: cfg} }

func (m ModelEntry) registryID() string {
	if m.ID != "" {
		return m.ID
	}
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = strings.TrimSpace(m.Alias)
	}
	if name == "" {
		return ""
	}
	return Provider + "/" + name
}

func (c *pluginConfig) validateModels() error {
	if c != nil && c.configErr != nil {
		return fmt.Errorf("invalid CommandCode configuration")
	}
	seen := map[string]bool{}
	for _, entry := range c.effectiveModels() {
		id := entry.registryID()
		if entry.ID != "" && (!strings.HasPrefix(id, Provider+"/") || strings.TrimSpace(strings.TrimPrefix(id, Provider+"/")) == "" || strings.TrimSpace(id) != id || strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Name) != entry.Name) {
			return fmt.Errorf("invalid explicit CommandCode model identity %q", id)
		}
		if id == "" || seen[strings.ToLower(id)] {
			return fmt.Errorf("invalid or duplicate CommandCode model %q", id)
		}
		seen[strings.ToLower(id)] = true
		if entry.Protocol != "" && entry.Protocol != "chat-completions" {
			return fmt.Errorf("CommandCode model %q uses unsupported protocol %q", id, entry.Protocol)
		}
		if entry.ContextLength < 0 || entry.MaxOutputTokens < 0 {
			return fmt.Errorf("negative limit for CommandCode model %q", id)
		}
	}
	return nil
}

func (p *ModelProvider) StaticModels(context.Context, pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	if err := p.cfg.validateModels(); err != nil {
		return pluginapi.ModelResponse{}, err
	}
	return pluginapi.ModelResponse{Provider: Provider, Models: p.models()}, nil
}

func (p *ModelProvider) ModelsForAuth(ctx context.Context, _ pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	return p.StaticModels(ctx, pluginapi.StaticModelRequest{})
}

func (p *ModelProvider) models() []pluginapi.ModelInfo {
	entries := p.cfg.effectiveModels()
	models := make([]pluginapi.ModelInfo, 0, len(entries))
	for _, entry := range entries {
		id := entry.registryID()
		label := entry.label() + " via CommandCode"
		model := pluginapi.ModelInfo{
			ID: id, Object: "model", OwnedBy: Provider, Type: "chat",
			DisplayName: label, Name: id, Description: label,
			ContextLength: entry.ContextLength, MaxCompletionTokens: entry.MaxOutputTokens,
			SupportedGenerationMethods: []string{"chatCompletions"},
			SupportedInputModalities:   append([]string(nil), entry.InputModalities...),
			SupportedOutputModalities:  append([]string(nil), entry.OutputModalities...),
			SupportedParameters:        []string{"temperature", "top_p", "max_tokens", "stop", "tools"},
		}
		if t := entry.Thinking; t != nil {
			model.Thinking = &pluginapi.ThinkingSupport{Min: t.Min, Max: t.Max, ZeroAllowed: t.ZeroAllowed, DynamicAllowed: t.DynamicAllowed, Levels: append([]string(nil), t.Levels...)}
			model.SupportedParameters = append(model.SupportedParameters, "reasoning_effort")
		}
		models = append(models, model)
	}
	return models
}
