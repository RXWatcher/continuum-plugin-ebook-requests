package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestConfigRedaction(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://u:sup3rsecret@db/x",
		BaseURL:     "https://ebookdb.internal",
		APIKey:      "TOPSECRETKEY",
	}
	if s := cfg.String(); strings.Contains(s, "sup3rsecret") || strings.Contains(s, "TOPSECRETKEY") {
		t.Fatalf("String leaked a secret: %s", s)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "config", cfg)
	if out := buf.String(); strings.Contains(out, "sup3rsecret") || strings.Contains(out, "TOPSECRETKEY") {
		t.Fatalf("slog leaked a secret: %s", out)
	} else if !strings.Contains(out, "ebookdb.internal") {
		t.Fatalf("redaction hid non-secret base_url: %s", out)
	}
}

func cfgReq(kv map[string]string) *pluginv1.ConfigureRequest {
	var items []*pluginv1.ConfigEntry
	for k, v := range kv {
		s, _ := structpb.NewStruct(map[string]any{"value": v})
		items = append(items, &pluginv1.ConfigEntry{Key: k, Value: s})
	}
	return &pluginv1.ConfigureRequest{Config: items}
}

func TestConfigure_RejectsInvalidBaseURL(t *testing.T) {
	s := New(nil, func(Config) error { return nil })
	for _, bad := range []string{"not-a-url", "ftp://x", "://nohost", "https://"} {
		_, err := s.Configure(context.Background(), cfgReq(map[string]string{
			"database_url": "postgres://x/y", "api_key": "k", "base_url": bad,
		}))
		if err == nil {
			t.Fatalf("base_url %q accepted, want rejected", bad)
		}
	}
	if _, err := s.Configure(context.Background(), cfgReq(map[string]string{
		"database_url": "postgres://x/y", "api_key": "k", "base_url": "https://ok.example",
	})); err != nil {
		t.Fatalf("valid base_url rejected: %v", err)
	}
}

func TestSnapshot_SliceIsolated(t *testing.T) {
	s := New(nil, func(Config) error { return nil })
	if _, err := s.Configure(context.Background(), cfgReq(map[string]string{
		"database_url": "postgres://x/y", "api_key": "k", "base_url": "https://ok.example",
	})); err != nil {
		t.Fatalf("configure: %v", err)
	}
	s.mu.Lock()
	s.cfg.ExternalSourcePriority = []string{"a", "b"}
	s.mu.Unlock()

	snap := s.Snapshot()
	if len(snap.ExternalSourcePriority) != 2 {
		t.Fatalf("snapshot slice = %v", snap.ExternalSourcePriority)
	}
	snap.ExternalSourcePriority[0] = "MUTATED"
	if again := s.Snapshot(); again.ExternalSourcePriority[0] != "a" {
		t.Fatalf("Snapshot aliases backing array: %v", again.ExternalSourcePriority)
	}
}
