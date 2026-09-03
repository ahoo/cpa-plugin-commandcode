package plugin

import (
	"gopkg.in/yaml.v3"
)

// pluginConfig mirrors the plugins.configs.<id> mapping the host hands us
// as raw YAML at register/reconfigure time. Unknown keys are preserved by
// the host (normalizedConfigNode keeps the mapping), so extra fields here
// are safe to add later.
type pluginConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Priority int      `yaml:"priority"`
	// Models optionally overrides the built-in commandcode model list.
	// Entries accept upstream names ("deepseek/deepseek-v4-flash") or bare
	// aliases ("deepseek-flash"); matching is prefix-insensitive.
	Models []string `yaml:"models"`
	// BaseURL overrides the upstream endpoint root (tests, mirrors).
	BaseURL string `yaml:"base_url"`
	// APIKey pins a static key; empty means "use the host-selected auth's
	// api_key attribute" (normal path: host picks a cmd-订阅 entry).
	APIKey string `yaml:"api_key"`
}

func parseConfig(raw []byte) *pluginConfig {
	cfg := &pluginConfig{}
	if len(raw) == 0 {
		return cfg
	}
	_ = yaml.Unmarshal(raw, cfg)
	return cfg
}

// modelSet returns the effective upstream model names (lower-cased,
// normalized) this plugin claims. Config override wins; default mirrors
// the cmd-订阅 openai-compatibility channel in config.yaml.
func (c *pluginConfig) modelSet() map[string]struct{} {
	if len(c.Models) > 0 {
		out := make(map[string]struct{}, len(c.Models))
		for _, m := range c.Models {
			if n := normalizeModel(m); n != "" {
				out[n] = struct{}{}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return map[string]struct{}{
		normalizeModel("deepseek/deepseek-v4-flash"):            {},
		normalizeModel("deepseek/deepseek-v4-flash-vision-exp"): {},
		normalizeModel("z-ai/glm-5.3-flash"):                    {},
		normalizeModel("deepseek-v4-flash"):                     {},
		normalizeModel("deepseek-v4-flash-vision-exp"):          {},
		normalizeModel("glm-5.3-flash"):                         {},
		// Host aliases clients actually request (cmd-订阅 channel).
		normalizeModel("deepseek-flash"): {},
		normalizeModel("deepseek-vision"): {},
	}
}

func (c *pluginConfig) baseURL() string {
	if c != nil && c.BaseURL != "" {
		return c.BaseURL
	}
	return upstreamBaseURL
}
