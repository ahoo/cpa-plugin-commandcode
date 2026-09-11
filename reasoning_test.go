package plugin

import (
	"strings"
	"testing"
)

func TestMapReasoningBody(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantRC     string
		wantChange bool
	}{
		{
			name:       "details array backfilled",
			in:         `{"choices":[{"delta":{"reasoning":"17","reasoning_details":[{"text":"17"}],"content":"hi"}}]}`,
			wantRC:     "17",
			wantChange: true,
		},
		{
			name:       "plain reasoning string backfilled",
			in:         `{"choices":[{"message":{"reasoning":"think-think","content":"done"}}]}`,
			wantRC:     "think-think",
			wantChange: true,
		},
		{
			name:       "existing reasoning_content untouched",
			in:         `{"choices":[{"delta":{"reasoning":"x","reasoning_content":"keep","content":"hi"}}]}`,
			wantRC:     "keep",
			wantChange: false,
		},
		{
			name:       "no reasoning fields passthrough",
			in:         `{"choices":[{"delta":{"content":"hi"}}]}`,
			wantRC:     "",
			wantChange: false,
		},
		{
			name:       "non-streaming message shape",
			in:         `{"id":"x","choices":[{"message":{"role":"assistant","reasoning":"deep","reasoning_details":[{"text":"deep"}],"content":"out"}}],"usage":{}}`,
			wantRC:     "deep",
			wantChange: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := mapReasoningBody([]byte(tc.in))
			if changed != tc.wantChange {
				t.Fatalf("changed=%v want %v (out=%s)", changed, tc.wantChange, out)
			}
			if tc.wantRC == "" {
				if changed && !strings.Contains(string(out), "reasoning_content") {
					t.Fatalf("expected no reasoning_content, got %s", out)
				}
				return
			}
			got := gjsonGetStringNested(out)
			if got != tc.wantRC {
				t.Fatalf("reasoning_content=%q want %q (out=%s)", got, tc.wantRC, out)
			}
		})
	}
}

func gjsonGetStringNested(out []byte) string {
	// find first occurrence under choices regardless of delta/message
	for _, field := range []string{"choices.0.delta.reasoning_content", "choices.0.message.reasoning_content"} {
		if v := gjsonGetString(out, field); v != "" {
			return v
		}
	}
	return ""
}

func TestNormalizeStreamLine(t *testing.T) {
	// bare-JSON contract: host adds "data: " framing downstream.
	if got := string(normalizeStreamLine([]byte("data: data: {\"a\":1}\n"))); got != "{\"a\":1}" {
		t.Fatalf("line normalize: %q", got)
	}
	if got := normalizeStreamLine([]byte("data: [DONE]\n")); len(got) != 0 {
		t.Fatalf("done not dropped: %q", got)
	}
	if got := normalizeStreamLine([]byte("\n")); len(got) != 0 {
		t.Fatalf("blank not dropped: %q", got)
	}
	if got := normalizeStreamLine([]byte(": ping\n")); len(got) != 0 {
		t.Fatalf("comment not dropped: %q", got)
	}
}

func TestNormalizeStreamBytes(t *testing.T) {
	raw := []byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"ab\",\"content\":\"x\"}}]}\n\ndata: [DONE]\n")
	out := normalizeStreamBytes(raw)
	if !strings.Contains(string(out), `"reasoning_content":"ab"`) {
		t.Fatalf("stream line not backfilled: %s", out)
	}
	if strings.Contains(string(out), "[DONE]") {
		t.Fatalf("[DONE] not swallowed: %s", out)
	}
	// double data: prefix collapsed, bare JSON out
	dbl := []byte("data: data: {\"choices\":[{\"delta\":{\"reasoning\":\"ab\",\"content\":\"x\"}}]}\n")
	dout := normalizeStreamBytes(dbl)
	if strings.Contains(string(dout), "data:") {
		t.Fatalf("framing leaked: %s", dout)
	}
	if !strings.Contains(string(dout), `"reasoning_content":"ab"`) {
		t.Fatalf("double-prefixed line not backfilled: %s", dout)
	}
	// partial frame: batch helper passes through (line buffering lives in
	// convertChunks); must never emit corrupt framing.
	partial := []byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"ab")
	if got := normalizeStreamBytes(partial); string(got) != string(partial) {
		t.Fatalf("partial frame modified: %s", got)
	}
}

// The alias MUST become the vendor's fully-qualified name: this plugin owns its
// executor and base URL, so the host's alias table never applies to its
// requests, and commandcode rejects a bare alias outright.
func TestDefaultMappingRewritesAliasToUpstream(t *testing.T) {
	tr := NewTranslator(&pluginConfig{})
	for alias, want := range map[string]string{
		"deepseek-flash": "deepseek/deepseek-v4.1-flash",
		"glm-5.3-flash":  "z-ai/glm-5.3-flash",
	} {
		body := []byte(`{"model":"` + alias + `"}`)
		got := tr.normalizeRequestModel(alias, body)
		if model := gjsonGetString(got, "model"); model != want {
			t.Errorf("model=%q, want %q", model, want)
		}
	}
}

// Overriding the mapping must take effect without any code change, so the
// vendor renaming a model is a configuration edit.
func TestConfiguredMappingOverridesDefault(t *testing.T) {
	cfg := parseConfig([]byte("models:\n  - alias: deepseek-flash\n    name: deepseek/deepseek-v9.9-flash\n"))
	tr := NewTranslator(cfg)
	got := tr.normalizeRequestModel("deepseek-flash", []byte(`{"model":"deepseek-flash"}`))
	if model := gjsonGetString(got, "model"); model != "deepseek/deepseek-v9.9-flash" {
		t.Fatalf("model=%q, want the configured upstream name", model)
	}
}

// An entry with no upstream claims the alias but forwards it verbatim, which is
// what a host-routed alias needs.
func TestEntryWithoutUpstreamForwardsVerbatim(t *testing.T) {
	cfg := parseConfig([]byte("models:\n  - deepseek-flash\n"))
	tr := NewTranslator(cfg)
	body := []byte(`{"model":"deepseek-flash"}`)
	if got := tr.normalizeRequestModel("deepseek-flash", body); string(got) != string(body) {
		t.Fatalf("model was rewritten: %s", got)
	}
	r := NewRouter(cfg)
	resp, _ := r.RouteModel(t.Context(), requestWithModel("deepseek-flash"))
	if !resp.Handled {
		t.Fatal("entry should still claim the model")
	}
}

func TestRouterOwned(t *testing.T) {
	r := NewRouter(&pluginConfig{})
	for _, m := range []string{
		"deepseek-flash",
		"deepseek-flash(high)",       // thinking suffix
		"commandcode/deepseek-flash", // provider prefix
		"glm-5.3-flash",
	} {
		resp, err := r.RouteModel(t.Context(), requestWithModel(m))
		if err != nil {
			t.Fatal(err)
		}
		if !resp.Handled {
			t.Errorf("model %q not routed", m)
		}
	}
	resp, _ := r.RouteModel(t.Context(), requestWithModel("gpt-5"))
	if resp.Handled {
		t.Errorf("unrelated model hijacked")
	}
}

// Both spellings of a mapped model are claimed: the client alias and the vendor
// name, so a request arriving either way reaches this executor.
func TestRouterOwnsConfiguredNames(t *testing.T) {
	cfg := parseConfig([]byte("models:\n  - alias: fast\n    name: deepseek/deepseek-v4.1-flash\n"))
	r := NewRouter(cfg)
	for _, m := range []string{"fast", "deepseek/deepseek-v4.1-flash"} {
		resp, _ := r.RouteModel(t.Context(), requestWithModel(m))
		if !resp.Handled {
			t.Errorf("model %q not routed", m)
		}
	}
}

// The vendor treats a path prefix as significant, so a name that normalizes to
// the same string as its alias must still be rewritten verbatim.
func TestPrefixSignificantVendorNameIsRewritten(t *testing.T) {
	tr := NewTranslator(&pluginConfig{})
	got := tr.normalizeRequestModel("glm-5.3-flash", []byte(`{"model":"glm-5.3-flash"}`))
	if model := gjsonGetString(got, "model"); model != "z-ai/glm-5.3-flash" {
		t.Fatalf("model=%q, want the prefixed vendor name", model)
	}
}
