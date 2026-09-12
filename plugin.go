// Package plugin implements the commandcode provider for CLIProxyAPI.
//
// Design: the host feeds this executor OpenAI chat-completions payloads
// (input format "openai", translated from claude/openai/responses by the
// host's own translators). The executor forwards them to
// https://api.commandcode.ai/provider/v1/chat/completions and normalizes
// the upstream response back into the standard OpenAI shape the host
// understands: commandcode returns reasoning under "reasoning" (string)
// and "reasoning_details[].text" but never "reasoning_content", which the
// host's openai->claude translator is blind to. Mapping here fixes the
// missing thinking block on /v1/messages without touching host code.
package plugin

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// Provider is the executor/model provider key. It must not collide with
	// any built-in provider key; native executors always win on collision.
	Provider = "commandcode"

	// executorFormat declares what this executor consumes and emits.
	// Both are OpenAI chat-completions JSON; the host translates to/from
	// claude/openai-responses/gemini/codex around us.
	executorFormat = "openai"

	// upstreamBaseURL is the commandcode OpenAI-compatible endpoint root.
	upstreamBaseURL = "https://api.commandcode.ai/provider/v1"
)

// pluginVersion tracks the release; cmd/commandcode/abi.go carries its own
// copy for registration metadata (injected via ldflags at release time).
const pluginVersion = "0.3.1-probe"

// CommandCodePlugin wires model metadata, routing, translation and execution.
type CommandCodePlugin struct {
	models     *ModelProvider
	router     *Router
	translator *Translator
	executor   *Executor
	cfg        *pluginConfig
}

// Build constructs the host-facing plugin description from the raw
// plugins.configs.<id> YAML the host passes at register/reconfigure time.
// It returns both the descriptor and the handler (the same *CommandCodePlugin
// backs every capability, so the ABI layer keeps one pointer).
func Build(configYAML []byte) (pluginapi.Plugin, *CommandCodePlugin) {
	cfg := parseConfig(configYAML)
	usageProbe.startup()
	p := &CommandCodePlugin{
		models: NewModelProvider(cfg),
		cfg:    cfg,
	}
	p.router = NewRouter(cfg)
	p.translator = NewTranslator(cfg)
	p.executor = NewExecutor(cfg, p.translator)
	desc := pluginapi.Plugin{
		Metadata: pluginapi.Metadata{
			Name:             "CommandCode Provider",
			Version:          pluginVersion,
			Author:           "cpa-admin",
			GitHubRepository: "https://github.com/ahoo/cpa-plugin-commandcode",
		},
		Capabilities: pluginapi.Capabilities{
			ModelProvider:         p.models,
			ModelRouter:           p.router,
			Executor:              p.executor,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeBoth,
			ExecutorInputFormats:  []string{executorFormat},
			ExecutorOutputFormats: []string{executorFormat},
			RequestTranslator:     p.translator,
			ResponseTranslator:    p.translator,
			UsagePlugin:           p,
		},
	}
	return desc, p
}

// Identifier returns the provider key.
func (p *CommandCodePlugin) Identifier() string { return Provider }

// StaticModels returns the commandcode models served through this executor.
func (p *CommandCodePlugin) StaticModels(ctx context.Context, req pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return p.models.StaticModels(ctx, req)
}

// ModelsForAuth mirrors static models; commandcode auths live on the host
// (openai-compatibility entries), so per-auth discovery is a no-op.
func (p *CommandCodePlugin) ModelsForAuth(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	return p.models.ModelsForAuth(ctx, req)
}

// RouteModel hijacks the commandcode-owned models to this plugin's executor.
func (p *CommandCodePlugin) RouteModel(ctx context.Context, req pluginapi.ModelRouteRequest) (pluginapi.ModelRouteResponse, error) {
	return p.router.RouteModel(ctx, req)
}

// TranslateRequest converts canonical requests into the commandcode envelope.
func (p *CommandCodePlugin) TranslateRequest(ctx context.Context, req pluginapi.RequestTransformRequest) (pluginapi.PayloadResponse, error) {
	return p.translator.TranslateRequest(ctx, req)
}

// TranslateResponse converts the commandcode envelope back to canonical shape.
func (p *CommandCodePlugin) TranslateResponse(ctx context.Context, req pluginapi.ResponseTransformRequest) (pluginapi.PayloadResponse, error) {
	return p.translator.TranslateResponse(ctx, req)
}

// Execute performs a non-streaming upstream call.
func (p *CommandCodePlugin) Execute(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	return p.executor.Execute(ctx, req)
}

// ExecuteStream performs a streaming upstream call.
func (p *CommandCodePlugin) ExecuteStream(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
	return p.executor.ExecuteStream(ctx, req)
}

// CountTokens estimates tokens without calling upstream.
func (p *CommandCodePlugin) CountTokens(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	return p.executor.CountTokens(ctx, req)
}

// HttpRequest bridges executor-owned raw HTTP through the host client.
func (p *CommandCodePlugin) HttpRequest(ctx context.Context, req pluginapi.ExecutorHTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	return p.executor.HttpRequest(ctx, req)
}

// HandleUsage receives usage records the host emits after a request completes.
//
// PROBE: this implementation only records that it was called, to determine
// whether the host publishes records for plugin-owned executors at all. If the
// host does not, the executor must publish its own usage instead.
func (p *CommandCodePlugin) HandleUsage(ctx context.Context, record pluginapi.UsageRecord) {
	_ = ctx
	usageProbe.record(record)
}

var (
	_ pluginapi.ModelProvider      = (*CommandCodePlugin)(nil)
	_ pluginapi.ModelRouter        = (*CommandCodePlugin)(nil)
	_ pluginapi.RequestTranslator  = (*CommandCodePlugin)(nil)
	_ pluginapi.ResponseTranslator = (*CommandCodePlugin)(nil)
	_ pluginapi.ProviderExecutor   = (*CommandCodePlugin)(nil)
	_ pluginapi.UsagePlugin        = (*CommandCodePlugin)(nil)
)

// normalizeModel strips provider prefixes, alias suffixes and whitespace so
// "deepseek-flash", "commandcode/deepseek-flash" and
// "deepseek/deepseek-v4-flash" compare equal downstream.
func normalizeModel(model string) string {
	m := strings.TrimSpace(model)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.Index(m, "("); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	return strings.ToLower(m)
}
