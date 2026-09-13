package plugin

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"reflect"
	"testing"
)

func TestConfiguredCapabilities(t *testing.T) {
	_, p := Build([]byte(`models:
 - name: vendor/vision
   alias: vision
   context_length: 98304
   max_output_tokens: 12345
   input_modalities: [text, image]
   output_modalities: [text]
   thinking:
     levels: [low, high]
 - name: vendor/unknown
`))
	catalog, err := p.StaticModels(context.Background(), pluginapi.StaticModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("catalog: %+v", catalog)
	}
	m := catalog.Models[0]
	if m.ID != "commandcode/vendor/vision" || m.ContextLength != 98304 || m.MaxCompletionTokens != 12345 || !reflect.DeepEqual(m.SupportedInputModalities, []string{"text", "image"}) || !reflect.DeepEqual(m.SupportedOutputModalities, []string{"text"}) || m.Thinking == nil || !reflect.DeepEqual(m.Thinking.Levels, []string{"low", "high"}) {
		t.Fatalf("metadata lost: %+v", m)
	}
	unknown := catalog.Models[1]
	if unknown.Thinking != nil || unknown.ContextLength != 0 || unknown.MaxCompletionTokens != 0 || len(unknown.SupportedInputModalities) != 0 || len(unknown.SupportedOutputModalities) != 0 {
		t.Fatalf("invented metadata: %+v", unknown)
	}
	m.Thinking.Levels[0] = "changed"
	m.SupportedInputModalities[0] = "changed"
	again, err := p.ModelsForAuth(context.Background(), pluginapi.AuthModelRequest{})
	if err != nil || again.Models[0].Thinking.Levels[0] != "low" || again.Models[0].SupportedInputModalities[0] != "text" {
		t.Fatal("returned metadata aliases configuration")
	}
}

func TestInvalidModelMetadataRejected(t *testing.T) {
	for _, raw := range []string{
		"models: [{name: vendor/model, protocol: messages}]",
		"models: [{name: vendor/model, context_length: -1}]",
		"models: [{name: vendor/model, max_output_tokens: -1}]",
		"models: [{name: vendor/model, context_length: wrong}]",
		"models: [{name: vendor/model}, {name: vendor/model}]",
	} {
		_, p := Build([]byte(raw))
		if _, err := p.StaticModels(context.Background(), pluginapi.StaticModelRequest{}); err == nil {
			t.Fatalf("invalid metadata accepted: %s", raw)
		}
		route, _ := p.RouteModel(context.Background(), pluginapi.ModelRouteRequest{RequestedModel: "vendor/model"})
		if route.Handled {
			t.Fatalf("invalid configuration claimed route: %s", raw)
		}
	}
}

func TestLegacyModelForms(t *testing.T) {
	for _, raw := range []string{"", "models: [vendor/model]", "models: [{alias: short, name: vendor/model}]"} {
		_, p := Build([]byte(raw))
		catalog, err := p.StaticModels(context.Background(), pluginapi.StaticModelRequest{})
		if err != nil || len(catalog.Models) == 0 {
			t.Fatalf("legacy config: %s, %v", raw, err)
		}
	}
}

func TestExplicitRegistryIdentityDoesNotClaimOtherProviders(t *testing.T) {
	_, p := Build([]byte(`models:
 - id: commandcode/custom
   name: vendor/exact-model
   alias: custom
`))
	catalog, err := p.StaticModels(context.Background(), pluginapi.StaticModelRequest{})
	if err != nil || catalog.Models[0].ID != "commandcode/custom" {
		t.Fatalf("catalog: %+v %v", catalog, err)
	}
	for model, want := range map[string]bool{"commandcode/custom": true, "other/custom": false, "custom": false, "vendor/exact-model": false, "commandcode/exact-model": false} {
		route, err := p.RouteModel(context.Background(), pluginapi.ModelRouteRequest{RequestedModel: model, Body: []byte(`{"model":"commandcode/custom"}`)})
		if err != nil || route.Handled != want {
			t.Fatalf("route %s: %+v %v", model, route, err)
		}
	}
	out, err := p.TranslateRequest(context.Background(), pluginapi.RequestTransformRequest{Model: "commandcode/custom", FromFormat: "openai", ToFormat: "commandcode", Body: []byte(`{"model":"commandcode/custom","messages":[]}`)})
	if err != nil || gjsonGetString(out.Body, "model") != "vendor/exact-model" {
		t.Fatalf("exact upstream identity lost: %s %v", out.Body, err)
	}
}

func TestInvalidExplicitIdentityRejected(t *testing.T) {
	for _, raw := range []string{
		"models: [{id: commandcode/, name: vendor/model}]",
		"models: [{id: ' commandcode/model', name: vendor/model}]",
		"models: [{id: other/model, name: vendor/model}]",
		"models: [{id: commandcode/model}]",
		"models: [{id: commandcode/model, name: ' vendor/model'}]",
		"models: [{id: commandcode/model, name: vendor/model}, {id: commandcode/MODEL, name: vendor/other}]",
	} {
		_, p := Build([]byte(raw))
		if _, err := p.StaticModels(context.Background(), pluginapi.StaticModelRequest{}); err == nil {
			t.Fatalf("invalid identity accepted: %s", raw)
		}
	}
}

func TestExactIdentityAcceptsThinkingSuffixesWithoutChangingVendorNames(t *testing.T) {
	_, p := Build([]byte(`models:
 - id: commandcode/custom
   name: vendor/model(preview)
 - id: commandcode/literal(high)
   name: vendor/literal-parenthesized
`))
	for _, suffix := range []string{"", "(minimal)", "(low)", "(medium)", "(HIGH)", "(xhigh)", "(max)", "(none)", "(auto)", "(-1)", "(8192)", "(0)"} {
		model := "commandcode/custom" + suffix
		want := "vendor/model(preview)"
		route, err := p.RouteModel(context.Background(), pluginapi.ModelRouteRequest{RequestedModel: model})
		if err != nil || !route.Handled {
			t.Fatalf("route %s: %+v %v", model, route, err)
		}
		body := p.executor.buildUpstreamBody(model, []byte(`{"model":"placeholder","messages":[],"reasoning_effort":"high"}`), false)
		if got := gjsonGetString(body, "model"); got != want {
			t.Fatalf("model %s: got %s, want %s", model, got, want)
		}
		if got := gjsonGetString(body, "reasoning_effort"); got != "high" {
			t.Fatalf("effort changed: %s", body)
		}
	}
	for _, model := range []string{"commandcode/custom(unknown)", "commandcode/custom()", "commandcode/custom(-2)", "commandcode/custom(high", "other/custom(high)", "custom(high)"} {
		route, err := p.RouteModel(context.Background(), pluginapi.ModelRouteRequest{RequestedModel: model, Body: []byte(`{"model":"commandcode/custom(low)"}`)})
		if err != nil || route.Handled {
			t.Fatalf("unowned route %s: %+v %v", model, route, err)
		}
		if name, ok := p.cfg.exactUpstreamName(model); ok {
			t.Fatalf("unowned upstream lookup %s: %s", model, name)
		}
	}
	if name, ok := p.cfg.exactUpstreamName("commandcode/literal(high)"); !ok || name != "vendor/literal-parenthesized" {
		t.Fatalf("literal parenthesized ID lost: %s %v", name, ok)
	}
	// With no separate model argument, routing may still use the request body.
	route, err := p.RouteModel(context.Background(), pluginapi.ModelRouteRequest{Body: []byte(`{"model":"commandcode/custom(low)"}`)})
	if err != nil || !route.Handled {
		t.Fatalf("body route: %+v %v", route, err)
	}
}
