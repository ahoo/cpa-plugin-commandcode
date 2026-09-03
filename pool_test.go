package plugin

import (
	"context"
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestMembersLegacySingleKey(t *testing.T) {
	cfg := &pluginConfig{APIKey: "  user_AAA "}
	ms := cfg.members(pluginapi.ExecutorRequest{})
	if len(ms) != 1 || ms[0].Key != "user_AAA" {
		t.Fatalf("legacy single key not resolved: %+v", ms)
	}
}

func TestMembersPoolWinsOverSingle(t *testing.T) {
	cfg := &pluginConfig{
		APIKey: "user_OLD",
		APIKeys: []APIKeyEntry{
			{Key: "user_A", Weight: 10, ProxyURL: "http://127.0.0.1:10080"},
			{Key: "", Weight: 5},
			{Key: "user_B"},
		},
	}
	ms := cfg.members(pluginapi.ExecutorRequest{})
	if len(ms) != 2 || ms[0].Key != "user_A" || ms[1].Key != "user_B" {
		t.Fatalf("pool not resolved (empty skipped): %+v", ms)
	}
}

func TestMembersAuthFallback(t *testing.T) {
	cfg := &pluginConfig{}
	req := pluginapi.ExecutorRequest{AuthAttributes: map[string]string{"api_key": "user_HOST"}}
	ms := cfg.members(req)
	if len(ms) != 1 || ms[0].Key != "user_HOST" {
		t.Fatalf("auth fallback not resolved: %+v", ms)
	}
	if len((&pluginConfig{}).members(pluginapi.ExecutorRequest{})) != 0 {
		t.Fatalf("empty config should yield no members")
	}
}

func TestOrderCoversAllMembers(t *testing.T) {
	p := newPool()
	members := []APIKeyEntry{{Key: "a", Weight: 10}, {Key: "b"}, {Key: "c", Weight: 3}}
	seen := map[int]int{}
	for i := 0; i < 200; i++ {
		ord := p.order(members)
		if len(ord) != 3 {
			t.Fatalf("order length: %v", ord)
		}
		uniq := map[int]bool{}
		for _, idx := range ord {
			uniq[idx] = true
		}
		if len(uniq) != 3 {
			t.Fatalf("order has repeats: %v", ord)
		}
		seen[ord[0]]++
	}
	// Weight 10 should win first pick most often; weight-1 members sometimes.
	if seen[0] < 100 {
		t.Fatalf("weighted first-pick distribution off: %v", seen)
	}
}

func TestClientForHostVsProxy(t *testing.T) {
	p := newPool()
	var hc pluginapi.HostHTTPClient = stubHostClient{}
	d, err := p.clientFor(0, APIKeyEntry{Key: "k"}, hc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.(hostDoer); !ok {
		t.Fatalf("no-proxy member should use host client, got %T", d)
	}
	if _, err := p.clientFor(0, APIKeyEntry{Key: "k"}, nil); err == nil {
		t.Fatalf("nil host client without proxy should error")
	}
	d, err = p.clientFor(1, APIKeyEntry{Key: "k", ProxyURL: "http://127.0.0.1:18080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.(stdDoer); !ok {
		t.Fatalf("proxy member should use std client, got %T", d)
	}
	if _, err := p.clientFor(2, APIKeyEntry{Key: "k", ProxyURL: "://bad"}, nil); err == nil {
		t.Fatalf("bad proxy_url should error")
	}
	if _, err := p.clientFor(3, APIKeyEntry{Key: "k", ProxyURL: "ftp://x"}, nil); err == nil {
		t.Fatalf("unsupported scheme should error")
	}
}

func TestRetryable(t *testing.T) {
	if !retryable(0, fmt.Errorf("dial")) {
		t.Errorf("transport error should retry")
	}
	if !retryable(401, nil) || !retryable(429, nil) || !retryable(500, nil) || !retryable(503, nil) {
		t.Errorf("401/429/5xx should retry")
	}
	if retryable(400, nil) || retryable(403, nil) || retryable(404, nil) || retryable(200, nil) {
		t.Errorf("other 4xx/2xx should not retry")
	}
}

// stubHostClient fails every call (used only for type-selection tests).
type stubHostClient struct{}

func (stubHostClient) Do(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return pluginapi.HTTPResponse{}, fmt.Errorf("stub")
}

func (stubHostClient) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{}, fmt.Errorf("stub")
}
