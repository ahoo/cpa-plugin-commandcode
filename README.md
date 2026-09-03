# cpa-plugin-commandcode

CLIProxyAPI native provider plugin for [commandcode.ai](https://api.commandcode.ai) (`api.commandcode.ai/provider/v1`).

CommandCode's OpenAI-compatible endpoint returns thinking text under `reasoning`
(string) and `reasoning_details[].text`, but never the standard
`reasoning_content` field. CLIProxyAPI's built-in openai→claude translator only
reads `reasoning_content`, so DeepSeek thinking vanishes on `/v1/messages`
even though the upstream produced it (`usage.reasoning_tokens > 0`). A
response-normalizer plugin cannot fix this: the openai-compat executor's
claude translation path never invokes response hooks.

This plugin implements a full provider (`ModelProvider + ModelRouter +
Executor + Request/Response translators`) that forwards chat-completions to
commandcode and normalizes every response back into standard OpenAI shape
before handing it to the host — so `/v1/messages` renders thinking blocks
without touching host code.

## Capabilities

- `model_provider` — static models: `commandcode/deepseek-v4-flash`,
  `commandcode/deepseek-v4-flash-vision-exp`, `commandcode/glm-5.3-flash`
  (namespaced so they never collide with the native openai-compatibility
  channel; the ABI has no live `/v1/models` discovery).
- `model_router` — hijacks `deepseek-flash`, `deepseek-vision`,
  `glm-5.3-flash` (and upstream-qualified names) to this executor.
- `executor` — POSTs to `/chat/completions` through the host HTTP client
  (proxy policy + request-log preserved); backfills `reasoning_content` on
  every non-streaming response and every SSE data line.
- `request_translator` / `response_translator` — alias→upstream model-name
  normalization and the same reasoning backfill for translated edges.

Streaming chunks are emitted as bare JSON — the host adds `data: ` framing
downstream. Empty lines and upstream `[DONE]` are swallowed (the host emits
its own stream tail). A line buffer reassembles SSE lines split across the
host's 32KB raw reads.

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
      models: ["deepseek/deepseek-v4-flash", "my-alias"]  # default: 3 built-ins + aliases
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
`plugins/linux/amd64/`.

## Test

```bash
go vet ./... && go test ./...
```

Covers the reasoning backfill (details-array priority, plain-string
fallback, existing-`reasoning_content` passthrough), the SSE line
normalizer (stacked `data:` collapse, `[DONE]`/blank swallowing),
alias→upstream model mapping, and router ownership.

## Release

1. `./build.sh` (or `PLUGIN_VERSION=x.y.z ./build.sh`).
2. Package `commandcode_<version>_<goos>_<goarch>.zip` files with the
   library at the zip root (Linux: `commandcode.so`, Darwin:
   `commandcode.dylib`, Windows: `commandcode.dll`).
3. Generate `checksums.txt` (sha256 of the zips).
4. `gh release create v<version> *.zip checksums.txt`

## License

MIT
