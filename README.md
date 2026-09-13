# cpa-plugin-commandcode

CLIProxyAPI native provider plugin for [commandcode.ai](https://api.commandcode.ai) (`api.commandcode.ai/provider/v1`).

CommandCode's OpenAI-compatible endpoint returns thinking text under `reasoning`
(string) and `reasoning_details[].text`, but never the standard
`reasoning_content` field. CLIProxyAPI's built-in openai→claude translator only
reads `reasoning_content`, so the field must be backfilled before translation.
For streaming `/v1/messages`, that translator also requires each OpenAI chunk
to arrive with an SSE `data: ` prefix; bare executor JSON is discarded.

This plugin implements a full provider (`ModelProvider + ModelRouter +
Executor + Request/Response translators`) that forwards chat-completions to
commandcode, normalizes responses into standard OpenAI shape, and applies the
route-specific transport framing expected by the host. This restores streaming
thinking, text, and usage on `/v1/messages` without modifying CLIProxyAPI.

## Capabilities

- `model_provider` — advertises the configured models under the
  `commandcode/` namespace (so they never collide with the native
  openai-compatibility channel; the ABI has no live `/v1/models` discovery).
- `model_router` — hijacks the configured client aliases (`deepseek-flash`,
  `deepseek-vision`, `glm-5.3-flash` by default) to this executor.
- `executor` — POSTs to `/chat/completions` through the host HTTP client
  (proxy policy + request-log preserved); backfills `reasoning_content` on
  every non-streaming response and every SSE data line, then applies the
  endpoint-specific stream framing described below.
- `request_translator` / `response_translator` — the same reasoning backfill
  for translated edges.

## Streaming compatibility

The host rewrites `ExecutorRequest.Format` and `SourceFormat` to the executor's
OpenAI format, so they cannot identify the original client endpoint. The
executor instead reads the host's public `request_path` metadata and prefixes a
normalized chunk with exactly one `data: ` only when its value is exactly
`/v1/messages`.

`/v1/chat/completions` and `/v1/responses` remain bare: the Chat Completions
handler adds HTTP SSE framing itself, while the Responses translation accepts
bare executor chunks. Missing, non-string, unknown, or near-match paths also
remain bare (fail closed), preventing `data: data: {...}` output. The plugin
strips stacked upstream prefixes before applying this policy and leaves the
host responsible for terminal events and `[DONE]`.

Only the separately mounted plugin artifact needs updating. No custom
CLIProxyAPI image and no Compose, updater, configuration, or `.env` changes are
required; official host auto-updates remain compatible.

## Model mapping

The plugin runs its own executor against `api.commandcode.ai`, so the host's
`openai-compatibility` alias table does not apply to its requests. commandcode
only accepts fully-qualified vendor names, and rejects a bare alias with
`Model "deepseek-flash" is not supported on this endpoint` — so the mapping has
to happen here.

Fields match the host's alias convention: `name` is what goes upstream,
`alias` is what clients send.

```yaml
    commandcode:
      models:
        - alias: deepseek-flash
          name: deepseek/deepseek-v4.1-flash
        - alias: glm-5.3-flash
          name: z-ai/glm-5.3-flash
          display_name: "GLM 5.3 Flash"     # optional label
```

When the vendor renames a model, edit this list — no code change. Omitting
`models` entirely uses the built-in defaults (those two entries).
An entry with no `name` claims the alias but forwards it verbatim, which is
only correct for aliases the host resolves itself.

## Install

```yaml
plugins:
  enabled: true
  configs:
    commandcode:
      enabled: true
      priority: 100
      api_keys:
        - key: user_YOUR_FIRST_KEY
          weight: 10
          proxy_url: http://127.0.0.1:18080   # optional per-key proxy
        - key: user_YOUR_SECOND_KEY
          weight: 5
          # no proxy_url -> host HTTP client (host proxy policy + request-log)
```

Legacy single-key form (`api_key: user_...`) still works and equals a
one-member pool. Weighted-random selection per request; transport errors,
401, 429 and 5xx fail over to the next member. Members with `proxy_url`
(http/https/socks5) use a self-built transport — host request-log cannot
capture those outbound calls.

On the ModelRouter path the host passes a nil auth to the executor, so the
key **must** come from `plugins.configs.commandcode.api_keys` (or legacy
`api_key`). Then restart:

```bash
docker restart cli-proxy-api
docker logs cli-proxy-api | grep commandcode
# pluginhost: plugin registered plugin_id=commandcode plugin_name=CommandCode Provider
```

Optional overrides:

```yaml
    commandcode:
      # Default: the three mappings shown under "Model mapping".
      models:
        - alias: my-alias
          name: vendor/model-name
      base_url: https://mirror.example.com/provider/v1    # default: https://api.commandcode.ai/provider/v1
```


## Build

Debian/glibc toolchain only (the runtime image is Debian; musl `.so` fails
to `dlopen`). Requires Go >= 1.26:

```bash
./build.sh
```

Runs `go vet`, `go test`, then `go build -buildmode=c-shared` for
`./cmd/commandcode`, emitting `commandcode-v<version>.so` into
`plugins/linux/amd64/`. The build injects the same version into the plugin's ABI
registration metadata; the default artifact and metadata version is `0.3.2`.

## Test

```bash
go vet ./... && go test ./...
```

Covers the reasoning backfill (details-array priority, plain-string
fallback, existing-`reasoning_content` passthrough), SSE buffering and
normalization (split reads, stacked `data:` collapse, malformed/control-line
filtering, `[DONE]` swallowing), exact-path framing policy, error/cancellation
propagation, alias→upstream model mapping, and router ownership.

## Release

1. `./build.sh` (or `PLUGIN_VERSION=x.y.z ./build.sh`).
2. Package `commandcode_<version>_<goos>_<goarch>.zip` files with the
   library at the zip root (Linux: `commandcode.so`, Darwin:
   `commandcode.dylib`, Windows: `commandcode.dll`).
3. Generate `checksums.txt` (sha256 of the zips).
4. `gh release create v<version> *.zip checksums.txt`

## License

MIT
