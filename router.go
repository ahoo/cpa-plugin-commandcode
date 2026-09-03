package plugin

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Router hijacks commandcode-owned models to this plugin's executor.
// The host consults routers in priority order before built-in provider
// resolution; returning Handled=false falls through to the normal
// openai-compatibility path (safe default during rollout).
type Router struct {
	cfg *pluginConfig
}

func NewRouter(cfg *pluginConfig) *Router { return &Router{cfg: cfg} }

func (r *Router) owned(req pluginapi.ModelRouteRequest) bool {
	set := r.cfg.modelSet()
	for _, candidate := range []string{req.RequestedModel, modelFromBody(req.Body)} {
		if candidate == "" {
			continue
		}
		if _, ok := set[normalizeModel(candidate)]; ok {
			return true
		}
		// Alias form: host aliases like "deepseek-flash" normalize to the
		// bare name, which is in the set via the config.yaml mirror entries.
	}
	return false
}

// RouteModel routes commandcode models to this plugin's own executor.
func (r *Router) RouteModel(ctx context.Context, req pluginapi.ModelRouteRequest) (pluginapi.ModelRouteResponse, error) {
	_ = ctx
	if !r.owned(req) {
		return pluginapi.ModelRouteResponse{}, nil
	}
	return pluginapi.ModelRouteResponse{
		Handled:    true,
		TargetKind: pluginapi.ModelRouteTargetSelf,
		Reason:     "commandcode provider plugin",
	}, nil
}

// modelFromBody extracts the model field from a raw client payload so the
// router also matches when RequestedModel is an alias the host already
// rewrote (or vice versa).
func modelFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	// Minimal scan without failing the route on malformed JSON.
	marker := []byte(`"model"`)
	idx := -1
	for i := 0; i+len(marker) <= len(body); i++ {
		match := true
		for j := range marker {
			if body[i+j] != marker[j] {
				match = false
				break
			}
		}
		if match {
			idx = i + len(marker)
			break
		}
	}
	if idx < 0 {
		return ""
	}
	rest := body[idx:]
	// skip whitespace, colon, whitespace, quote
	p := 0
	for p < len(rest) && (rest[p] == ' ' || rest[p] == '\t' || rest[p] == '\r' || rest[p] == '\n' || rest[p] == ':') {
		p++
	}
	if p >= len(rest) || rest[p] != '"' {
		return ""
	}
	p++
	var b strings.Builder
	for ; p < len(rest); p++ {
		c := rest[p]
		if c == '\\' && p+1 < len(rest) {
			p++
			b.WriteByte(rest[p])
			continue
		}
		if c == '"' {
			return b.String()
		}
		b.WriteByte(c)
	}
	return ""
}
