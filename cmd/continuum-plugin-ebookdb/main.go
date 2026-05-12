package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	goruntime "runtime"
	"sync/atomic"

	"github.com/hashicorp/go-hclog"
	"github.com/jackc/pgx/v5/pgxpool"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	publicmanifest "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/runtime"

	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/consumer"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/event"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/httproutes"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/migrate"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/reconciler"
	pluginrt "github.com/ContinuumApp/continuum-plugin-ebookdb/internal/runtime"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/scheduler"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/server"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/store"
)

//go:embed manifest.json
var manifestRaw []byte

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "continuum-plugin-ebookdb"})

	manifest, err := loadManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}

	httpSrv := httproutes.NewServer()

	var (
		poolPtr       atomic.Pointer[pgxpool.Pool]
		consumerDepsP atomic.Pointer[consumer.Deps]
		reconcilerPtr atomic.Pointer[reconciler.Reconciler]
	)

	consumerHandler := consumer.New(func() *consumer.Deps { return consumerDepsP.Load() })
	schedulerSrv := scheduler.New(func() *reconciler.Reconciler { return reconcilerPtr.Load() })

	rt := pluginrt.New(manifest, func(cfg pluginrt.Config) error {
		ctx := context.Background()
		p, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("pgxpool: %w", err)
		}
		if err := migrate.Run(ctx, cfg.DatabaseURL); err != nil {
			p.Close()
			return fmt.Errorf("migrate: %w", err)
		}
		st := store.New(p)
		ebkClient := ebookdb.NewClient(cfg.BaseURL, cfg.APIKey)

		srv := server.New(server.Deps{EbookDBClient: ebkClient})
		httpSrv.SetHandler(srv.Handler())

		ev := event.New(sdkruntime.Host(), logger.Named("event"))
		consumerDepsP.Store(&consumer.Deps{
			Store: st, Pub: ev, EBK: ebkClient,
			PluginID: "continuum.ebookdb",
		})
		reconcilerPtr.Store(reconciler.New(reconciler.Deps{
			Store: st, Pub: ev, EBK: ebkClient,
		}))

		if old := poolPtr.Swap(p); old != nil {
			old.Close()
		}
		logger.Info("configured", "base_url", cfg.BaseURL)
		return nil
	})

	sdkruntime.Serve(sdkruntime.ServeConfig{
		Logger: logger,
		Servers: sdkruntime.CapabilityServers{
			Runtime:       rt,
			HttpRoutes:    httpSrv,
			EventConsumer: consumerHandler,
			ScheduledTask: schedulerSrv,
		},
	})
}

func loadManifest() (*pluginv1.PluginManifest, error) {
	manifest, err := publicmanifest.Load(manifestRaw)
	if err != nil {
		return nil, fmt.Errorf("load embedded manifest: %w", err)
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	binaryData, err := os.ReadFile(executablePath)
	if err != nil {
		return nil, fmt.Errorf("read executable %q: %w", executablePath, err)
	}
	checksum := sha256.Sum256(binaryData)
	manifest.Checksum = hex.EncodeToString(checksum[:])
	if len(manifest.GetSupportedPlatforms()) == 0 {
		manifest.SupportedPlatforms = []*pluginv1.SupportedPlatform{
			{Os: goruntime.GOOS, Arch: goruntime.GOARCH},
		}
	}
	return manifest, nil
}
