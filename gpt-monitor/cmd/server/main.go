package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"gpt-pro-monitor/internal/auth"
	"gpt-pro-monitor/internal/config"
	"gpt-pro-monitor/internal/db"
	"gpt-pro-monitor/internal/httpapi"
	"gpt-pro-monitor/internal/iq"
	"gpt-pro-monitor/internal/monitor"
	"gpt-pro-monitor/internal/webui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hash-password":
			if err := hashPassword(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "healthcheck":
			if err := healthcheck(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	poolConfig.MaxConns = cfg.DBMaxConns
	poolConfig.MinConns = 0
	// 硬性护栏：任何一条查询最多占用 DB_STATEMENT_TIMEOUT，
	// 监控再怎么出错也不可能把 46 的 Postgres 挂住；application_name 便于在
	// pg_stat_activity 里一眼认出是谁在查。
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(int(cfg.DBStatementTimeout.Milliseconds()))
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "gpt-pro-monitor"
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	authManager, err := auth.NewManager(auth.Config{
		Username:      cfg.AdminUsername,
		PasswordHash:  cfg.AdminPasswordHash,
		SessionSecret: cfg.SessionSecret,
		SessionTTL:    cfg.SessionTTL,
		CookieSecure:  cfg.CookieSecure,
		MaxAttempts:   cfg.LoginRateLimitAttempts,
		AttemptWindow: cfg.LoginRateLimitWindow,
	})
	if err != nil {
		return fmt.Errorf("create auth manager: %w", err)
	}

	repository := db.NewAccountRepository(pool)
	monitorService := monitor.NewService(repository, monitor.Options{
		CacheTTL:              cfg.CacheTTL,
		UsageStaleAfter:       cfg.UsageStaleAfter,
		UsageWarningThreshold: cfg.UsageWarningThreshold,
		CriticalExpiryDays:    cfg.CriticalExpiryDays,
		WarningExpiryDays:     cfg.WarningExpiryDays,
		AttentionExpiryDays:   cfg.AttentionExpiryDays,
		UpcomingExpiryDays:    cfg.UpcomingExpiryDays,
		AdminURL:              cfg.Sub2APIAdminURL,
		Now:                   time.Now,
	})

	var codexService *monitor.CodexService
	if cfg.CodexEnabled {
		codexRepository := db.NewCodexRepository(pool, cfg.CodexDisplayTimezone)
		codexService = monitor.NewCodexService(codexRepository, monitor.CodexOptions{
			PoolCacheTTL:     cfg.CodexPoolCacheTTL,
			UsageCacheTTL:    cfg.CodexUsageCacheTTL,
			TrendCacheTTL:    cfg.CodexTrendCacheTTL,
			SampleInterval:   cfg.CodexSampleInterval,
			TrendRetention:   cfg.CodexTrendRetention,
			UsageWindowHours: cfg.CodexUsageWindowHours,
			TrendDays:        cfg.CodexTrendDays,
			PoolGroupIDs:     cfg.CodexPoolGroupIDs,
			DefaultModels:    cfg.CodexGatedModels,
			UsageStaleAfter:  cfg.UsageStaleAfter,
			Now:              time.Now,
		})
	}

	samplerCtx, stopSampler := context.WithCancel(context.Background())
	defer stopSampler()
	if codexService != nil {
		// 采样器只打票池那条便宜查询（账号表 ~110 行），
		// 用来补「持票账号数随时间变化」这一维 —— 票是覆盖写的，DB 里没有历史。
		go codexService.RunSampler(samplerCtx, func(err error) {
			logger.Warn("codex trend sample failed", "error", err)
		})
	}

	var iqService *iq.Service
	iqDone := make(chan struct{})
	if cfg.IQEnabled {
		iqService, err = newIQService(cfg, pool, logger)
		if err != nil {
			return err
		}
		defer iqService.Close()
		go func() {
			defer close(iqDone)
			iqService.Run(samplerCtx)
		}()
		logger.Info("iq probe enabled", "sub2api", cfg.Sub2APIBaseURL, "min_version", cfg.IQMinVersion,
			"interval", cfg.IQInterval.String(), "jitter", cfg.IQJitter.String(), "db", cfg.IQDBPath)
	} else {
		close(iqDone)
	}

	handler := httpapi.NewHandler(httpapi.Options{
		Monitor: monitorService,
		Codex:   codexServiceOrNil(codexService),
		IQ:      iqServiceOrNil(iqService),
		Health:  pool,
		Auth:    authManager,
		WebFS:   webui.FS(),
		Logger:  logger,
	})
	server := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", cfg.ServerAddr)
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case signalValue := <-signals:
		logger.Info("shutdown requested", "signal", signalValue.String())
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	// 停调度器并等在途的测试收尾（已拿到的答案照样落盘），最多等到 ShutdownTimeout。
	stopSampler()
	select {
	case <-iqDone:
	case <-shutdownCtx.Done():
		logger.Warn("iq probes still running at shutdown")
	}
	return nil
}

func newIQService(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*iq.Service, error) {
	defaults, err := iq.ParseModelSpecs(cfg.IQDefaultModels)
	if err != nil || len(defaults) == 0 {
		return nil, fmt.Errorf("IQ_DEFAULT_MODELS: %v", err)
	}
	// 锚点按看板的显示时区对齐（config 里已校验过这个时区能加载）。
	location, err := time.LoadLocation(cfg.CodexDisplayTimezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone: %w", err)
	}
	store, err := iq.OpenStore(cfg.IQDBPath)
	if err != nil {
		return nil, err
	}
	client := iq.NewClient(cfg.Sub2APIBaseURL, cfg.Sub2APIAdminKey, cfg.IQRequestTimeout)
	service := iq.NewService(store, db.NewIQRepository(pool), client, iq.Options{
		Interval:       cfg.IQInterval,
		Jitter:         cfg.IQJitter,
		Concurrency:    cfg.IQConcurrency,
		RequestTimeout: cfg.IQRequestTimeout,
		Retention:      cfg.IQRetention,
		MinVersion:     cfg.IQMinVersion,
		DefaultModels:  defaults,
		ApplyTicket:    cfg.IQApplyTicket,
		RecentWindow:   cfg.IQRecentWindow,
		MinSamples:     cfg.IQMinSamples,
		HealthyPercent: cfg.IQHealthyPercent,
		WarningPercent: cfg.IQWarningPercent,
		ClassifyModel:  monitor.ClassifyResponseModel,
		Location:       location,
		Logger:         logger,
	})
	if cfg.IQProvidersEnabled {
		sealer, err := iq.NewSealer(cfg.IQSecretKey)
		if err != nil {
			return nil, fmt.Errorf("IQ_SECRET_KEY: %w", err)
		}
		service.EnableProviders(iq.NewOpenAIClient(cfg.IQRequestTimeout, cfg.IQProviderAllowPrivate), sealer)
	}
	return service, nil
}

// iqServiceOrNil 同 codexServiceOrNil：避免 nil 指针被包成非 nil 接口值。
func iqServiceOrNil(service *iq.Service) httpapi.IQService {
	if service == nil {
		return nil
	}
	return service
}

func hashPassword() error {
	var password string
	if len(os.Args) > 2 {
		password = os.Args[2]
	} else {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		password = strings.TrimRight(string(raw), "\r\n")
	}
	if len(password) < 12 {
		return fmt.Errorf("password must contain at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	fmt.Println(string(hash))
	return nil
}

func healthcheck() error {
	url := strings.TrimSpace(os.Getenv("HEALTHCHECK_URL"))
	if url == "" {
		url = "http://127.0.0.1:8080/healthz"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("healthcheck status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// codexServiceOrNil 保证 nil 的 *CodexService 不会被包成非 nil 接口值，
// 否则 handler 里的 o.Codex == nil 永远不成立。
func codexServiceOrNil(service *monitor.CodexService) httpapi.CodexService {
	if service == nil {
		return nil
	}
	return service
}
