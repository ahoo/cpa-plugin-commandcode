package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Translator converts between host canonical formats and the commandcode
// envelope. Since commandcode speaks OpenAI chat-completions natively, the
// request direction is a light pass-through (model-name normalization only)
// and the response direction is the reasoning backfill in reasoning.go.
type Translator struct {
	cfg *pluginConfig
}

func NewTranslator(cfg *pluginConfig) *Translator { return &Translator{cfg: cfg} }

// TranslateRequest handles canonical -> commandcode. Supported edges:
// openai->commandcode, claude->commandcode, openai-response->commandcode.
// The host usually feeds us openai already (executor input format); other
// edges arriving here are passed through untouched rather than failed so a
// new host format never 500s live traffic.
func (t *Translator) TranslateRequest(ctx context.Context, req pluginapi.RequestTransformRequest) (pluginapi.PayloadResponse, error) {
	_ = ctx
	from := strings.ToLower(strings.TrimSpace(req.FromFormat))
	to := strings.ToLower(strings.TrimSpace(req.ToFormat))
	if to != "" && to != "commandcode" && to != "openai" {
		return pluginapi.PayloadResponse{}, fmt.Errorf("unsupported request translation %s -> %s", req.FromFormat, req.ToFormat)
	}
	switch from {
	case "", "openai", "commandcode":
		return pluginapi.PayloadResponse{Body: normalizeRequestModel(req.Model, req.Body)}, nil
	default:
		return pluginapi.PayloadResponse{Body: append([]byte(nil), req.Body...)}, nil
	}
}

// TranslateResponse handles commandcode -> canonical. Supported edges:
// commandcode->openai, commandcode->claude, commandcode->openai-response.
// The reasoning backfill runs for every edge (it is format-agnostic JSON);
// the host's own translators then render thinking blocks downstream.
func (t *Translator) TranslateResponse(ctx context.Context, req pluginapi.ResponseTransformRequest) (pluginapi.PayloadResponse, error) {
	_ = ctx
	from := strings.ToLower(strings.TrimSpace(req.FromFormat))
	to := strings.ToLower(strings.TrimSpace(req.ToFormat))
	if from != "" && from != "commandcode" && from != "openai" {
		return pluginapi.PayloadResponse{}, fmt.Errorf("unsupported response translation %s -> %s", req.FromFormat, req.ToFormat)
	}
	if to != "" && to != "openai" && to != "claude" && to != "openai-response" && to != "commandcode" {
		return pluginapi.PayloadResponse{}, fmt.Errorf("unsupported response translation %s -> %s", req.FromFormat, req.ToFormat)
	}
	fixed, _ := mapReasoningBody(req.Body)
	return pluginapi.PayloadResponse{Body: fixed}, nil
}

// normalizeRequestModel ensures the outbound model field carries the upstream
// name (e.g. "deepseek/deepseek-v4-flash") rather than a host alias, so
// commandcode routes to the right weights even if alias stripping drifted.
func normalizeRequestModel(model string, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	upstream := upstreamModelName(model)
	if upstream == "" {
		return body
	}
	current := gjsonGetString(body, "model")
	if normalizeModel(current) == normalizeModel(upstream) {
		return body
	}
	if out, err := sjsonSetString(body, "model", upstream); err == nil {
		return out
	}
	return body
}

// upstreamModelName maps a host-facing alias back to the commandcode model id.
func upstreamModelName(model string) string {
	switch normalizeModel(model) {
	case "deepseek-flash", "deepseek-v4-flash":
		return "deepseek/deepseek-v4-flash"
	case "deepseek-vision", "deepseek-v4-flash-vision-exp":
		return "deepseek/deepseek-v4-flash-vision-exp"
	case "glm-5.3-flash", "glm-5-3-flash":
		return "z-ai/glm-5.3-flash"
	}
	// Already an upstream-qualified name? keep verbatim.
	if strings.Contains(model, "/") {
		return strings.TrimSpace(model)
	}
	return ""
}
