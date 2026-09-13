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
