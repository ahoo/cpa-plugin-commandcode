package plugin

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// ModelProvider contributes the commandcode model list to the host registry.
// The ABI only offers static + per-auth discovery (no live /v1/models crawl:
// StaticModels has no HTTPClient), so the list is static by design and must
// be kept in sync with the cmd-订阅 openai-compatibility channel.
type ModelProvider struct{}

func NewModelProvider() *ModelProvider { return &ModelProvider{} }

type modelDef struct {
	id          string
	displayName string
}

// NOTE: IDs use the commandcode/ namespace deliberately. The host's native
// openai-compatibility channel (cmd-订阅) already registers
// deepseek/deepseek-v4-flash etc.; RegisterExecutors skips plugin models
// that any native executor serves (modelHasNativeExecutor), so reusing the
// upstream IDs would leave this executor permanently unregistered. The
// router matches client aliases/upstream names to this executor, so clients
// keep requesting deepseek-flash unchanged.
var staticModelDefs = []modelDef{
	{"commandcode/deepseek-v4-flash", "DeepSeek V4 Flash via CommandCode"},
	{"commandcode/deepseek-v4-flash-vision-exp", "DeepSeek V4 Flash Vision via CommandCode"},
	{"commandcode/glm-5.3-flash", "GLM 5.3 Flash via CommandCode"},
}

func (p *ModelProvider) StaticModels(context.Context, pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: Provider, Models: staticModels()}, nil
}

func (p *ModelProvider) ModelsForAuth(context.Context, pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: Provider, Models: staticModels()}, nil
}

func staticModels() []pluginapi.ModelInfo {
	models := make([]pluginapi.ModelInfo, 0, len(staticModelDefs))
	for _, def := range staticModelDefs {
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
