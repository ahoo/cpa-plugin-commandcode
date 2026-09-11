package plugin

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// pluginConfig mirrors the plugins.configs.<id> mapping the host hands us
// as raw YAML at register/reconfigure time. Unknown keys are preserved by
// the host (normalizedConfigNode keeps the mapping), so extra fields here
// are safe to add later.
type pluginConfig struct {
	Enabled  bool `yaml:"enabled"`
	Priority int  `yaml:"priority"`
	// Models declares which models this plugin claims. Entries are structured
	// ("- alias: x / upstream: y") or bare strings ("- x"); a bare string only
	// claims the name and never rewrites it.
	Models []ModelEntry `yaml:"models"`
	// BaseURL overrides the upstream endpoint root (tests, mirrors).
	BaseURL string `yaml:"base_url"`
	// APIKey pins a single static key (v0.1.x compatible). Prefer APIKeys.
	// NOTE: on the ModelRouter path the host passes a nil auth, so the key
	// MUST come from plugin config, not host auth selection.
	APIKey string `yaml:"api_key"`
	// APIKeys is the v0.2.0 multi-key pool: weighted selection with
	// per-key proxy and failover retry. When non-empty it wins over APIKey.
	APIKeys []APIKeyEntry `yaml:"api_keys"`

	// Derived from Models at parse time (see buildIndexes). Not YAML fields.
	claimed  map[string]struct{}
	rewrites map[string]string
}

// ModelEntry maps a client-facing alias to the name the vendor serves, using
// the same field names as the host's openai-compatibility channel so one mental
// model covers both:
//
//	name  — the model name sent upstream ("deepseek/deepseek-v4.1-flash")
//	alias — the name clients request ("deepseek-flash")
//
// The mapping is REQUIRED for this plugin's executor: it uses its own base URL,
// so the host's alias table never applies to its requests, and commandcode
// rejects a bare alias. Leaving Name empty forwards the client's name verbatim,
// which is only correct for aliases the host itself resolves.
type ModelEntry struct {
	// Alias is the client-facing name, e.g. "deepseek-flash".
	Alias string `yaml:"alias"`
	// Name is the model name sent upstream. Empty forwards Alias unchanged.
	Name string `yaml:"name"`
	// DisplayName is the optional label for model registration; falls back to
	// Name, then Alias.
	DisplayName string `yaml:"display_name"`
}

// UnmarshalYAML accepts both the structured mapping and the legacy bare string
// form, so existing configurations keep working unchanged.
func (m *ModelEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		m.Alias = strings.TrimSpace(node.Value)
		return nil
	}
	type plain ModelEntry
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*m = ModelEntry(decoded)
	return nil
}

// label resolves the human-readable label for model registration.
func (m ModelEntry) label() string {
	if label := strings.TrimSpace(m.DisplayName); label != "" {
		return label
	}
	if name := strings.TrimSpace(m.Name); name != "" {
		return name
	}
	return strings.TrimSpace(m.Alias)
}

// APIKeyEntry is one pool member: key + weight + optional per-key proxy.
type APIKeyEntry struct {
	Key      string `yaml:"key"`
	Weight   int    `yaml:"weight"`
	ProxyURL string `yaml:"proxy_url"`
}

func (en APIKeyEntry) normWeight() int {
	if en.Weight <= 0 {
		return 1
	}
	return en.Weight
}

// defaultModelEntries are the built-in alias -> upstream mapping.
//
// The mapping is REQUIRED, not cosmetic: this plugin owns its own executor and
// base URL, so the host's openai-compatibility alias table never applies to the
// requests it sends. commandcode only accepts fully-qualified upstream names
// ("deepseek/deepseek-v4.1-flash"), and rejects a bare client alias with
// `Model "deepseek-flash" is not supported on this endpoint`.
//
// These values are only defaults. Whenever the vendor renames a model, override
// them in plugins.configs.commandcode.models — no code change needed:
//
//	models:
//	  - alias: deepseek-flash
//	    upstream: deepseek/deepseek-v4.1-flash
func defaultModelEntries() []ModelEntry {
	return []ModelEntry{
		{Alias: "deepseek-flash", Name: "deepseek/deepseek-v4.1-flash"},
		{Alias: "glm-5.3-flash", Name: "z-ai/glm-5.3-flash"},
	}
}

func parseConfig(raw []byte) *pluginConfig {
	cfg := &pluginConfig{}
	if len(raw) == 0 {
		cfg.buildIndexes()
		return cfg
	}
	_ = yaml.Unmarshal(raw, cfg)
	cfg.buildIndexes()
	return cfg
}

// effectiveModels returns the configured entries, or the built-in defaults
// when configuration declares none.
func (c *pluginConfig) effectiveModels() []ModelEntry {
	if c != nil && len(c.Models) > 0 {
		return c.Models
	}
	return defaultModelEntries()
}

// buildIndexes derives the lookup tables once per configuration so request
// handling stays allocation-free. Called from parseConfig only.
//
// Keys are built with normalizeModel so a client's "commandcode/deepseek-flash"
// or "deepseek-flash(high)" matches the plain entry. Values are the operator's
// literal Name: it is the vendor's identifier and must never be normalized, or
// writing "z-ai/glm-5.3-flash" would silently become "glm-5.3-flash" and the
// upstream would reject it.
func (c *pluginConfig) buildIndexes() {
	entries := c.effectiveModels()
	c.claimed = make(map[string]struct{}, len(entries)*2)
	c.rewrites = make(map[string]string, len(entries)*2)
	for _, entry := range entries {
		alias := normalizeModel(entry.Alias)
		name := strings.TrimSpace(entry.Name)
		if alias != "" {
			c.claimed[alias] = struct{}{}
		}
		if name == "" {
			// No upstream name: forward verbatim, claim the alias only.
			continue
		}
		normalizedName := normalizeModel(name)
		if normalizedName != "" {
			c.claimed[normalizedName] = struct{}{}
		}
		// Both spellings resolve to the vendor's literal name, so a request
		// arriving as either the alias or the upstream name is rewritten.
		if alias != "" {
			c.rewrites[alias] = name
		}
		if normalizedName != "" {
			c.rewrites[normalizedName] = name
		}
	}
}

// ensureIndexes builds the lookup tables if they are missing. Constructors call
// it so a zero-value pluginConfig (notably in tests) still behaves like the
// default configuration instead of silently claiming nothing. It must not run
// concurrently with request handling: configurations are parsed once at
// register/reconfigure and then only read.
func (c *pluginConfig) ensureIndexes() {
	if c != nil && c.claimed == nil {
		c.buildIndexes()
	}
}

// modelSet returns the normalized names this plugin claims, whether they are
// client aliases or upstream names. A claim never implies a rewrite.
func (c *pluginConfig) modelSet() map[string]struct{} {
	if c == nil || c.claimed == nil {
		return map[string]struct{}{}
	}
	return c.claimed
}

// upstreamName returns the vendor's model name for a client-requested model.
// An empty result means "do not rewrite": the request keeps the client's name,
// which is only correct for aliases the host resolves itself.
func (c *pluginConfig) upstreamName(model string) string {
	if c == nil || len(c.rewrites) == 0 {
		return ""
	}
	return c.rewrites[normalizeModel(model)]
}

func (c *pluginConfig) baseURL() string {
	if c != nil && c.BaseURL != "" {
		return c.BaseURL
	}
	return upstreamBaseURL
}
