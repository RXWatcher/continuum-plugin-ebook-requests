// Package runtime implements the plugin's Runtime gRPC server.
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	"github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/runtimedefault"
)

// Config is the parsed plugin global config (per spec Layer 9.3).
type Config struct {
	DatabaseURL            string
	BaseURL                string
	APIKey                 string
	DefaultCoverSize       string
	ExternalSourcePriority []string
}

func (c Config) Configured() bool {
	return c.BaseURL != "" && c.APIKey != "" && c.DatabaseURL != ""
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	return "***redacted***"
}

// LogValue implements slog.LogValuer so slog.Any("cfg", c) never serializes
// the API key or the DSN (which embeds the DB password).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("database_url", mask(c.DatabaseURL)),
		slog.String("base_url", c.BaseURL),
		slog.String("api_key", mask(c.APIKey)),
		slog.String("default_cover_size", c.DefaultCoverSize),
		slog.Any("external_source_priority", c.ExternalSourcePriority),
	)
}

// String implements fmt.Stringer with the same redaction so fmt/log of the
// config is also safe.
func (c Config) String() string { return c.LogValue().String() }

type Server struct {
	runtimedefault.Server
	manifest *pluginv1.PluginManifest
	onCfg    func(Config) error

	mu  sync.RWMutex
	cfg Config
}

func New(manifest *pluginv1.PluginManifest, onConfig func(Config) error) *Server {
	return &Server{manifest: manifest, onCfg: onConfig}
}

func (s *Server) GetManifest(_ context.Context, _ *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *Server) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	cfg := Config{}
	for _, e := range req.GetConfig() {
		v := e.GetValue()
		if v == nil {
			continue
		}
		m := v.AsMap()
		switch e.GetKey() {
		case "database_url":
			cfg.DatabaseURL = stringFromValue(m["value"])
		case "base_url":
			cfg.BaseURL = stringFromValue(m["value"])
		case "api_key":
			cfg.APIKey = stringFromValue(m["value"])
		case "default_cover_size":
			cfg.DefaultCoverSize = stringFromValue(m["value"])
		case "external_source_priority":
			cfg.ExternalSourcePriority = stringSliceFromValue(m["value"])
		}
	}
	if cfg.DatabaseURL == "" {
		s.mu.Lock()
		s.cfg = cfg
		s.mu.Unlock()
		return &pluginv1.ConfigureResponse{}, nil
	}
	if !cfg.Configured() {
		s.mu.Lock()
		s.cfg = cfg
		s.mu.Unlock()
		return &pluginv1.ConfigureResponse{}, nil
	}
	if u, err := url.Parse(cfg.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base_url must be a valid http(s) URL")
	}
	if s.onCfg != nil {
		if err := s.onCfg(cfg); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return &pluginv1.ConfigureResponse{}, nil
}

func (s *Server) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	// Deep-copy the slice so a caller can't mutate the shared backing array
	// while a concurrent Configure rewrites s.cfg.
	c.ExternalSourcePriority = append([]string(nil), s.cfg.ExternalSourcePriority...)
	return c
}

func stringFromValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func stringSliceFromValue(v any) []string {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(a))
	for _, e := range a {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
