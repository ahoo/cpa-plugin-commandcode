package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestStreamFramingForRequest(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     streamFramingPolicy
	}{
		{name: "claude messages", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: "/v1/messages"}, want: streamFramingClaude},
		{name: "chat completions", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: "/v1/chat/completions"}, want: streamFramingBare},
		{name: "responses", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: "/v1/responses"}, want: streamFramingBare},
		{name: "nil metadata", metadata: nil, want: streamFramingBare},
		{name: "missing path", metadata: map[string]any{}, want: streamFramingBare},
		{name: "nil path", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: nil}, want: streamFramingBare},
		{name: "numeric path", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: 1}, want: streamFramingBare},
		{name: "boolean path", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: true}, want: streamFramingBare},
		{name: "map path", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: map[string]any{"path": "/v1/messages"}}, want: streamFramingBare},
		{name: "slice path", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: []string{"/v1/messages"}}, want: streamFramingBare},
		{name: "empty path", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: ""}, want: streamFramingBare},
		{name: "whitespace padded", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: " /v1/messages "}, want: streamFramingBare},
		{name: "case changed", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: "/V1/messages"}, want: streamFramingBare},
		{name: "query bearing", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: "/v1/messages?beta=true"}, want: streamFramingBare},
		{name: "speculative prefix", metadata: map[string]any{coreexecutor.RequestPathMetadataKey: "/anthropic/v1/messages"}, want: streamFramingBare},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := streamFramingForRequest(pluginapi.ExecutorRequest{Metadata: tt.metadata})
			if got != tt.want {
				t.Fatalf("streamFramingForRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecuteStreamWiresRequestPathFraming(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantPrefix bool
	}{
		{name: "Claude Messages", path: "/v1/messages", wantPrefix: true},
		{name: "Chat Completions", path: "/v1/chat/completions", wantPrefix: false},
		{name: "Responses", path: "/v1/responses", wantPrefix: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := NewExecutor(parseConfig([]byte("api_key: test-key\n")), nil)
			client := staticStreamHTTPClient{payloads: [][]byte{
				[]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"think\"}}]}\n"),
				[]byte("data: [DONE]\n"),
			}}
			response, err := executor.ExecuteStream(t.Context(), pluginapi.ExecutorRequest{
				Model:      "deepseek-flash",
				Payload:    []byte(`{"model":"deepseek-flash","messages":[]}`),
				Metadata:   map[string]any{coreexecutor.RequestPathMetadataKey: tt.path},
				HTTPClient: client,
			})
			if err != nil {
				t.Fatal(err)
			}
			var chunks []pluginapi.ExecutorStreamChunk
			for chunk := range response.Chunks {
				chunks = append(chunks, chunk)
			}
			if len(chunks) != 1 {
				t.Fatalf("chunk count = %d, want one normalized payload", len(chunks))
			}
			if chunks[0].Err != nil {
				t.Fatalf("unexpected stream error: %v", chunks[0].Err)
			}
			hasPrefix := bytes.HasPrefix(chunks[0].Payload, []byte("data: "))
			if hasPrefix != tt.wantPrefix {
				t.Fatalf("payload = %q, data prefix=%v, want %v", chunks[0].Payload, hasPrefix, tt.wantPrefix)
			}
			body := chunks[0].Payload
			if hasPrefix {
				body = body[len("data: "):]
			}
			if !json.Valid(body) || !bytes.Contains(body, []byte(`"reasoning_content":"think"`)) {
				t.Fatalf("payload was not normalized before framing: %q", chunks[0].Payload)
			}
		})
	}
}

func TestConvertChunksAppliesFramingAfterNormalization(t *testing.T) {
	input := []pluginapi.HTTPStreamChunk{
		{Payload: []byte("da")},
		{Payload: []byte("ta: {\"choices\":[{\"delta\":{\"reasoning\":\"think\"}}]}\r\n")},
		{Payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\ndata: data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n")},
		{Payload: []byte("\nevent: ignored\n: ping\ndata: not-json\n")},
		{Payload: []byte("data: [DONE]\n")},
		{Payload: []byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]} ")},
	}

	bare := collectConvertedChunks(t, input, streamFramingBare)
	claude := collectConvertedChunks(t, input, streamFramingClaude)
	if len(bare) != 4 || len(claude) != len(bare) {
		t.Fatalf("chunk counts: bare=%d claude=%d, want 4 each", len(bare), len(claude))
	}

	for i := range bare {
		if bare[i].Err != nil || claude[i].Err != nil {
			t.Fatalf("chunk %d unexpectedly contains an error: bare=%v claude=%v", i, bare[i].Err, claude[i].Err)
		}
		if bytes.HasPrefix(bare[i].Payload, []byte("data:")) {
			t.Fatalf("bare chunk %d contains framing: %q", i, bare[i].Payload)
		}
		if !bytes.HasPrefix(claude[i].Payload, []byte("data: ")) {
			t.Fatalf("Claude chunk %d lacks framing: %q", i, claude[i].Payload)
		}
		claudeJSON := claude[i].Payload[len("data: "):]
		if bytes.HasPrefix(bytes.TrimSpace(claudeJSON), []byte("data:")) {
			t.Fatalf("Claude chunk %d is double framed: %q", i, claude[i].Payload)
		}
		if !bytes.Equal(claudeJSON, bare[i].Payload) {
			t.Fatalf("Claude chunk %d body = %q, want bare body %q", i, claudeJSON, bare[i].Payload)
		}
		if !json.Valid(claudeJSON) {
			t.Fatalf("Claude chunk %d body is invalid JSON: %q", i, claudeJSON)
		}
	}

	joined := string(bytes.Join(payloadsOf(bare), []byte("\n")))
	if !strings.Contains(joined, `"reasoning_content":"think"`) {
		t.Fatalf("reasoning was not backfilled before framing: %s", joined)
	}
	if !strings.Contains(joined, `"content":"answer"`) {
		t.Fatalf("content was not preserved: %s", joined)
	}
	if !strings.Contains(joined, `"prompt_tokens":11`) || !strings.Contains(joined, `"completion_tokens":7`) {
		t.Fatalf("usage was not preserved: %s", joined)
	}
	if strings.Contains(joined, "[DONE]") || strings.Contains(joined, "not-json") || strings.Contains(joined, "ignored") || strings.Contains(joined, "ping") {
		t.Fatalf("filtered upstream data leaked: %s", joined)
	}
}

func TestConvertChunksForwardsTerminalErrorWithoutFraming(t *testing.T) {
	sentinel := errors.New("upstream read failed")
	input := []pluginapi.HTTPStreamChunk{
		{Payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"before\"}}]}\n")},
		{Payload: []byte("data: {\"should_not\":\"frame\"}"), Err: sentinel},
		{Payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"after\"}}]}\n")},
	}

	got := collectConvertedChunks(t, input, streamFramingClaude)
	if len(got) != 2 {
		t.Fatalf("chunk count = %d, want payload followed by terminal error", len(got))
	}
	if !bytes.HasPrefix(got[0].Payload, []byte("data: ")) || got[0].Err != nil {
		t.Fatalf("first chunk = %#v, want framed payload", got[0])
	}
	if !errors.Is(got[1].Err, sentinel) {
		t.Fatalf("terminal error = %v, want %v", got[1].Err, sentinel)
	}
	if len(got[1].Payload) != 0 {
		t.Fatalf("terminal error payload was framed: %q", got[1].Payload)
	}
}

func TestConvertChunksClosesAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan pluginapi.HTTPStreamChunk)
	output := convertChunks(ctx, input, streamFramingClaude)
	cancel()

	select {
	case chunk, ok := <-output:
		if !ok || !errors.Is(chunk.Err, context.Canceled) {
			t.Fatalf("cancellation chunk = %#v, open=%v", chunk, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("converter did not report cancellation")
	}
	select {
	case _, ok := <-output:
		if ok {
			t.Fatal("converter emitted data after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("converter did not close after cancellation")
	}
	close(input)
}

func collectConvertedChunks(t *testing.T, chunks []pluginapi.HTTPStreamChunk, framing streamFramingPolicy) []pluginapi.ExecutorStreamChunk {
	t.Helper()
	input := make(chan pluginapi.HTTPStreamChunk, len(chunks))
	for _, chunk := range chunks {
		input <- chunk
	}
	close(input)

	var output []pluginapi.ExecutorStreamChunk
	for chunk := range convertChunks(context.Background(), input, framing) {
		output = append(output, chunk)
	}
	return output
}

func payloadsOf(chunks []pluginapi.ExecutorStreamChunk) [][]byte {
	payloads := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		payloads = append(payloads, chunk.Payload)
	}
	return payloads
}

type staticStreamHTTPClient struct {
	payloads [][]byte
}

func (staticStreamHTTPClient) Do(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return pluginapi.HTTPResponse{}, errors.New("unexpected non-streaming request")
}

func (c staticStreamHTTPClient) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	chunks := make(chan pluginapi.HTTPStreamChunk, len(c.payloads))
	for _, payload := range c.payloads {
		chunks <- pluginapi.HTTPStreamChunk{Payload: append([]byte(nil), payload...)}
	}
	close(chunks)
	return pluginapi.HTTPStreamResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/event-stream"}},
		Chunks:     chunks,
	}, nil
}
