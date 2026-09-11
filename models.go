package plugin

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// ModelProvider contributes the commandcode model list to the host registry.
// The ABI only offers static + per-auth discovery (no live /v1/models crawl:
// StaticModels has no HTTPClient), so the list is derived from the same
// configuration that drives routing, keeping the two in step automatically.
type ModelProvider struct {
	cfg *pluginConfig
}

func NewModelProvider(cfg *pluginConfig) *ModelProvider { return &ModelProvider{cfg: cfg} }

// modelDef is the registry-facing shape of one claimed model.
type modelDef struct {
	id          string
	displayName string
}

// registryModels builds the advertised list from configuration.
//
// NOTE: IDs use the commandcode/ namespace deliberately. The host's native
// openai-compatibility channel (cmd-订阅) already registers the bare upstream
// names; RegisterExecutors skips plugin models that any native executor serves
// (modelHasNativeExecutor), so reusing those IDs would leave this executor
// permanently unregistered. The router matches client aliases to this
// executor, so clients keep requesting deepseek-flash unchanged.
func (p *ModelProvider) registryModels() []modelDef {
	entries := p.cfg.effectiveModels()
	defs := make([]modelDef, 0, len(entries))
	for _, entry := range entries {
		// Prefer the upstream name for the ID: it is the name the vendor
		// actually serves, so the advertised model stays meaningful across
		// alias changes. Fall back to the alias when no upstream is declared.
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = strings.TrimSpace(entry.Alias)
		}
		if name == "" {
			continue
		}
		defs = append(defs, modelDef{
			id:          Provider + "/" + name,
			displayName: entry.label() + " via CommandCode",
		})
	}
	return defs
}

func (p *ModelProvider) StaticModels(context.Context, pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: Provider, Models: p.models()}, nil
}

func (p *ModelProvider) ModelsForAuth(context.Context, pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: Provider, Models: p.models()}, nil
}

func (p *ModelProvider) models() []pluginapi.ModelInfo {
	defs := p.registryModels()
	models := make([]pluginapi.ModelInfo, 0, len(defs))
	for _, def := range defs {
		models = append(models, pluginapi.ModelInfo{
			ID:                         def.id,
			Object:                     "model",
			OwnedBy:                    "commandcode",
			Type:                       "chat",
			DisplayName:                def.displayName,
			Name:                       def.id,
			Description:                def.displayName,
			SupportedGenerationMethods: []string{"chatCompletions"},
			SupportedInputModalities:   []string{"text"},
			SupportedOutputModalities:  []string{"text"},
			SupportedParameters:        []string{"temperature", "top_p", "max_tokens", "stop", "tools", "reasoning_effort"},
			Thinking: &pluginapi.ThinkingSupport{
				DynamicAllowed: true,
				Levels:         []string{"none", "auto", "low", "medium", "high", "max"},
			},
		})
	}
	return models
}
