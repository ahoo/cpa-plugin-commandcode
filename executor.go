package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/sjson"
)

// Executor forwards OpenAI chat-completions payloads to commandcode and
// normalizes responses back into standard OpenAI shape (reasoning backfill).
// All upstream I/O goes through req.HTTPClient so host proxy policy,
// per-auth proxy-url and request-log capture keep working.
type Executor struct {
	cfg        *pluginConfig
	translator *Translator
}

func NewExecutor(cfg *pluginConfig, t *Translator) *Executor {
	if t == nil {
		t = NewTranslator(cfg)
	}
	return &Executor{cfg: cfg, translator: t}
}

func (e *Executor) Identifier() string { return Provider }

// apiKey resolves credentials. NOTE: on the ModelRouter path the host
// calls the executor with a nil auth (executor_route.go passes
// (*coreauth.Auth)(nil)), so AuthAttributes/AuthMetadata are always empty
// here. The key MUST come from plugins.configs.<id>.api_key (or the
// host-level proxy default is irrelevant — commandcode needs a Bearer key).
func apiKey(cfg *pluginConfig, req pluginapi.ExecutorRequest) string {
	if cfg != nil && strings.TrimSpace(cfg.APIKey) != "" {
		return strings.TrimSpace(cfg.APIKey)
	}
	if req.AuthAttributes != nil {
		if k := strings.TrimSpace(req.AuthAttributes["api_key"]); k != "" {
			return k
		}
	}
	if req.AuthMetadata != nil {
		if k, ok := req.AuthMetadata["api_key"].(string); ok && strings.TrimSpace(k) != "" {
			return strings.TrimSpace(k)
		}
	}
	return ""
}

const missingKeyMsg = "commandcode executor: missing api key (router path passes nil auth; set plugins.configs.commandcode.api_key in config.yaml)"

func (e *Executor) endpoint() string {
	return strings.TrimSuffix(e.cfg.baseURL(), "/") + "/chat/completions"
}

// buildUpstreamBody runs the request translator edge openai->commandcode so
// model normalization stays in one place, then forces stream flags.
func (e *Executor) buildUpstreamBody(model string, payload []byte, stream bool) []byte {
	out, err := e.translator.TranslateRequest(context.Background(), pluginapi.RequestTransformRequest{
		FromFormat: "openai",
		ToFormat:   "commandcode",
		Model:      model,
		Stream:     stream,
		Body:       payload,
	})
	body := payload
	if err == nil && len(out.Body) > 0 {
		body = out.Body
	}
	return setStreamFlag(body, stream)
}

func setStreamFlag(body []byte, stream bool) []byte {
	if len(body) == 0 {
		return body
	}
	updated, err := sjson.SetBytes(body, "stream", stream)
	if err != nil {
		return body
	}
	body = updated
	if stream {
		if updated, err := sjson.SetBytes(body, "stream_options.include_usage", true); err == nil {
			body = updated
		}
	}
	return body
}

func upstreamHeaders(apiKey string, stream bool) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+apiKey)
	h.Set("User-Agent", "cli-proxy-commandcode")
	if stream {
		h.Set("Accept", "text/event-stream")
	} else {
		h.Set("Accept", "application/json")
	}
	return h
}

// Execute performs a non-streaming chat completion.
func (e *Executor) Execute(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	if req.HTTPClient == nil {
		return pluginapi.ExecutorResponse{}, fmt.Errorf("commandcode executor: host HTTP client is required")
	}
	key := apiKey(e.cfg, req)
	if key == "" {
		return pluginapi.ExecutorResponse{}, statusError{statusCode: http.StatusUnauthorized, msg: missingKeyMsg}
	}
	body := e.buildUpstreamBody(req.Model, req.Payload, false)
	resp, err := req.HTTPClient.Do(ctx, pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     e.endpoint(),
		Headers: upstreamHeaders(key, false),
		Body:    body,
	})
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pluginapi.ExecutorResponse{}, statusError{statusCode: resp.StatusCode, body: resp.Body}
	}
	fixed, _ := mapReasoningBody(resp.Body)
	return pluginapi.ExecutorResponse{Payload: fixed, Headers: resp.Headers}, nil
}

// ExecuteStream performs a streaming chat completion, normalizing each SSE
// data line before handing chunks back. The host pumps them downstream via
// host.stream.emit; returning converted (not raw) chunks is supported —
// gemini-cli's convertHTTPChunks does the same unwrap on this path.
func (e *Executor) ExecuteStream(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
	if req.HTTPClient == nil {
		return pluginapi.ExecutorStreamResponse{}, fmt.Errorf("commandcode executor: host HTTP client is required")
	}
	key := apiKey(e.cfg, req)
	if key == "" {
		return pluginapi.ExecutorStreamResponse{}, statusError{statusCode: http.StatusUnauthorized, msg: missingKeyMsg}
	}
	body := e.buildUpstreamBody(req.Model, req.Payload, true)
	resp, err := req.HTTPClient.DoStream(ctx, pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     e.endpoint(),
		Headers: upstreamHeaders(key, true),
		Body:    body,
	})
	if err != nil {
		return pluginapi.ExecutorStreamResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pluginapi.ExecutorStreamResponse{}, statusError{statusCode: resp.StatusCode, body: readStreamErrorBody(ctx, resp.Chunks)}
	}
	return pluginapi.ExecutorStreamResponse{Headers: resp.Headers, Chunks: convertChunks(ctx, resp.Chunks)}, nil
}

// convertChunks normalizes each upstream SSE data payload (reasoning
// backfill) and re-wraps it as an executor chunk.
//
// Framing contract (verified live): the host adds the "data: " prefix when
// delivering executor chunks downstream, so chunks MUST be bare JSON without
// any SSE framing. Empty lines are dropped, the upstream [DONE] is swallowed
// (the host emits its own stream tail).
//
// The host delivers arbitrary 32KB raw reads, so one SSE line can straddle
// two chunks. We buffer until a newline completes the line; only complete
// lines go through normalizeStreamLine. The tail remainder is flushed when
// the upstream closes.
func convertChunks(ctx context.Context, in <-chan pluginapi.HTTPStreamChunk) <-chan pluginapi.ExecutorStreamChunk {
	out := make(chan pluginapi.ExecutorStreamChunk)
	go func() {
		defer close(out)
		var pending []byte
		emit := func(payload []byte) bool {
			if len(bytes.TrimSpace(payload)) == 0 {
				return true
			}
			select {
			case <-ctx.Done():
				return false
			case out <- pluginapi.ExecutorStreamChunk{Payload: payload}:
				return true
			}
		}
		for {
			select {
			case <-ctx.Done():
				out <- pluginapi.ExecutorStreamChunk{Err: ctx.Err()}
				return
			case chunk, ok := <-in:
				if !ok {
					if frame := normalizeStreamLine(pending); len(bytes.TrimSpace(frame)) > 0 {
						emit(frame)
					}
					return
				}
				if chunk.Err != nil {
					out <- pluginapi.ExecutorStreamChunk{Err: chunk.Err}
					return
				}
				pending = append(pending, chunk.Payload...)
				for {
					idx := bytes.IndexByte(pending, '\n')
					if idx < 0 {
						break
					}
					line := pending[:idx+1]
					pending = pending[idx+1:]
					if !emit(normalizeStreamLine(line)) {
						return
					}
				}
				// Guard against unbounded growth on a never-ending line.
				if len(pending) > 4<<20 {
					if !emit(normalizeStreamLine(pending)) {
						return
					}
					pending = nil
				}
			}
		}
	}()
	return out
}

// normalizeStreamLine converts one complete upstream SSE line into the bare
// JSON payload the host expects (host adds "data: " framing downstream).
// Stacked prefixes are collapsed, reasoning_content is backfilled, empty
// lines and [DONE] yield nil (dropped; host owns stream termination).
func normalizeStreamLine(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		// Non-SSE bytes (e.g. event: lines, comments): drop, never corrupt.
		return nil
	}
	payload := trimmed
	for bytes.HasPrefix(bytes.TrimSpace(payload), []byte("data:")) {
		p := bytes.TrimSpace(payload)
		payload = bytes.TrimSpace(p[len("data:"):])
	}
	if len(payload) == 0 || string(payload) == "[DONE]" {
		return nil
	}
	if !json.Valid(payload) {
		return nil
	}
	if fixed, ok := mapReasoningBody(payload); ok {
		return fixed
	}
	return payload
}

// normalizeStreamBytes handles one raw host-stream read (kept for unit
// tests and non-streaming helpers): same bare-JSON contract as
// normalizeStreamLine, joined back with newlines for assertion convenience.
func normalizeStreamBytes(raw []byte) []byte {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw
	}
	lines := bytes.Split(raw, []byte("\n"))
	var out [][]byte
	for _, line := range lines {
		if frame := normalizeStreamLine(line); len(bytes.TrimSpace(frame)) > 0 {
			out = append(out, frame)
		}
	}
	if len(out) == 0 {
		return raw
	}
	return bytes.Join(out, []byte("\n"))
}

// CountTokens is a local estimate; commandcode exposes no tokenize endpoint.
func (e *Executor) CountTokens(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	_ = ctx
	count := int64(len(req.Payload) / 4)
	if count < 1 && len(req.Payload) > 0 {
		count = 1
	}
	usage := map[string]any{
		"prompt_tokens":     count,
		"completion_tokens": 0,
		"total_tokens":      count,
	}
	raw, _ := json.Marshal(map[string]any{
		"id":      "commandcode-count",
		"object":  "chat.completion",
		"created": 0,
		"model":   req.Model,
		"choices": []any{},
		"usage":   usage,
	})
	return pluginapi.ExecutorResponse{Payload: raw}, nil
}

// HttpRequest bridges raw executor HTTP through the host client with the
// resolved api key injected.
func (e *Executor) HttpRequest(ctx context.Context, req pluginapi.ExecutorHTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	if req.HTTPClient == nil {
		return pluginapi.ExecutorHTTPResponse{}, fmt.Errorf("commandcode executor: host HTTP client is required")
	}
	if strings.TrimSpace(req.URL) == "" {
		return pluginapi.ExecutorHTTPResponse{}, fmt.Errorf("commandcode executor: request URL is required")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	headers := req.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	if headers.Get("Authorization") == "" {
		key := ""
		if e.cfg != nil && strings.TrimSpace(e.cfg.APIKey) != "" {
			key = strings.TrimSpace(e.cfg.APIKey)
		} else if req.Attributes != nil {
			key = strings.TrimSpace(req.Attributes["api_key"])
		}
		if key == "" {
			return pluginapi.ExecutorHTTPResponse{}, statusError{statusCode: http.StatusUnauthorized, msg: missingKeyMsg}
		}
		headers.Set("Authorization", "Bearer "+key)
	}
	resp, err := req.HTTPClient.Do(ctx, pluginapi.HTTPRequest{
		Method:  method,
		URL:     req.URL,
		Headers: headers,
		Body:    req.Body,
	})
	if err != nil {
		return pluginapi.ExecutorHTTPResponse{}, err
	}
	return pluginapi.ExecutorHTTPResponse{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body}, nil
}

// statusError carries an upstream HTTP status back to the host (the ABI
// error envelope preserves it as http_status for retry classification).
type statusError struct {
	statusCode int
	msg        string
	body       []byte
}

func (e statusError) Error() string {
	if strings.TrimSpace(e.msg) != "" {
		return e.msg
	}
	if len(e.body) > 0 {
		return upstreamErrorMessage(e.body)
	}
	return fmt.Sprintf("status %d", e.statusCode)
}

func (e statusError) StatusCode() int { return e.statusCode }

func upstreamErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var decoded struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		if len(decoded.Error) > 0 {
			var obj struct {
				Message string `json:"message"`
			}
			if errObj := json.Unmarshal(decoded.Error, &obj); errObj == nil && strings.TrimSpace(obj.Message) != "" {
				return strings.TrimSpace(obj.Message)
			}
			var s string
			if errStr := json.Unmarshal(decoded.Error, &s); errStr == nil && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		if strings.TrimSpace(decoded.Message) != "" {
			return strings.TrimSpace(decoded.Message)
		}
	}
	if len(trimmed) > 500 {
		return trimmed[:500]
	}
	return trimmed
}

func readStreamErrorBody(ctx context.Context, chunks <-chan pluginapi.HTTPStreamChunk) []byte {
	const maxBytes = 1 << 20
	body := make([]byte, 0)
	if chunks == nil {
		return body
	}
	for len(body) < maxBytes {
		select {
		case <-ctx.Done():
			return body
		case chunk, ok := <-chunks:
			if !ok {
				return body
			}
			if len(chunk.Payload) > 0 {
				remaining := maxBytes - len(body)
				if len(chunk.Payload) > remaining {
					return append(body, chunk.Payload[:remaining]...)
				}
				body = append(body, chunk.Payload...)
			}
			if chunk.Err != nil {
				return body
			}
		}
	}
	return body
}
