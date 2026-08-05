package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/release"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Application struct {
	Config       config.Config
	Logger       *slog.Logger
	Location     *time.Location
	Pool         *pgxpool.Pool
	Sessions     *scs.SessionManager
	SessionStore *pgxstore.PostgresStore
	ObjectStore  storage.ObjectStore
	Server       *http.Server
}

func New(ctx context.Context) (*Application, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	logger := newLogger(cfg, os.Stdout)
	slog.SetDefault(logger)

	location, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		return nil, fmt.Errorf("load Europe/Lisbon: %w", err)
	}

	databaseURL, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse DATABASE_URL")
	}
	poolConfig.MaxConns = cfg.DBMaxConns
	poolConfig.MinConns = cfg.DBMinConns
	poolConfig.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.DBMaxConnIdleTime
	poolConfig.HealthCheckPeriod = cfg.DBHealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("open database pool")
	}
	pingContext, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingContext); err != nil {
		pool.Close()
		return nil, errors.New("database ping failed")
	}

	sessionStore := pgxstore.New(pool)
	sessions := scs.New()
	sessions.Store = sessionStore
	sessions.Lifetime = cfg.SessionLifetime
	sessions.IdleTimeout = cfg.SessionIdleTimeout
	sessions.Cookie.Name = "mycfc_session"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.Path = "/"
	sessions.Cookie.SameSite = http.SameSiteLaxMode
	sessions.Cookie.Secure = cfg.IsProduction()
	sessions.Cookie.Domain = cfg.CookieDomain

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		sessionStore.StopCleanup()
		pool.Close()
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	s3Client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.S3ForcePathStyle
		if cfg.S3Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.S3Endpoint)
		}
	})
	objectStore := storage.NewS3Store(s3Client, cfg.S3BucketName)

	csrfKey, err := cfg.CSRFAuthKey()
	if err != nil {
		sessionStore.StopCleanup()
		pool.Close()
		return nil, err
	}

	assets, err := loadAssetManifest()
	if err != nil {
		sessionStore.StopCleanup()
		pool.Close()
		return nil, err
	}
	landing := handlers.Landing{
		PageMeta: components.PageMeta{Title: "Clube Fluvial de Coimbra | MyCFC", StylesheetURL: assets["app.css"], ScriptURL: assets["app.js"], BrandImageURL: assets["images/cfc-logo.png"]},
		HeroURL:  assets["images/cfc-hero.png"],
	}
	login := handlers.Login{
		Users:    dbgen.New(pool),
		Sessions: sessions,
		PageMeta: components.PageMeta{
			StylesheetURL: assets["app.css"],
			ScriptURL:     assets["app.js"],
			BrandImageURL: assets["images/cfc-logo.png"],
		},
	}
	registration := handlers.Registration{
		Store: handlers.PostgresRegistrationStore{Pool: pool}, Sessions: sessions, Location: location,
		TermsVersion: cfg.ConsentTermsVersion, TermsSHA256: cfg.ConsentTermsSHA256,
		ImageVersion: cfg.ConsentImageVersion, ImageSHA256: cfg.ConsentImageSHA256,
		TermsURL: cfg.ConsentTermsURL, ImageURL: cfg.ConsentImageURL,
		PageMeta: components.PageMeta{StylesheetURL: assets["app.css"], ScriptURL: assets["app.js"], BrandImageURL: assets["images/cfc-logo.png"]},
	}
	pageMeta := components.PageMeta{
		StylesheetURL: assets["app.css"],
		ScriptURL:     assets["app.js"],
		BrandImageURL: assets["images/cfc-logo.png"],
	}
	system := handlers.System{PageMeta: pageMeta}
	var appReleasedAt time.Time
	if cfg.AppReleasedAt != "" {
		appReleasedAt, _ = time.Parse(time.RFC3339, cfg.AppReleasedAt)
	}
	releaseChecker := release.NewChecker(&http.Client{Timeout: cfg.ReleaseCheckTimeout}, cfg.ReleaseRepository, cfg.AppVersion, cfg.GITSHA, appReleasedAt, cfg.ReleaseCheckCacheTTL, time.Now)
	dashboard := handlers.Dashboard{
		Store:                 dbgen.New(pool),
		Fleet:                 dbgen.New(pool),
		Equipment:             dbgen.New(pool),
		Releases:              releaseChecker,
		Objects:               objectStore,
		MaxRequestBytes:       cfg.MaxRequestBytes,
		MaxPhotoBytes:         cfg.MaxPhotoBytes,
		Dependents:            handlers.PostgresGuardianDependentStore{Pool: pool},
		PageMeta:              pageMeta,
		System:                system,
		Location:              location,
		Sessions:              sessions,
		ResponsibilityVersion: cfg.ConsentMinorVersion, ResponsibilitySHA256: cfg.ConsentMinorSHA256,
		ResponsibilityURL: cfg.ConsentMinorURL,
		CompetitionID:     cfg.CalendarCompetitionID, TrainingID: cfg.CalendarTrainingID,
		SocialID: cfg.CalendarSocialID, CleanupsID: cfg.CalendarCleanupsID,
		CalendarAPIKey: cfg.GoogleCalendarAPIKey,
	}
	auth := handlers.Auth{Users: dbgen.New(pool), Sessions: sessions, System: system}
	repair := handlers.Repair{Store: dbgen.New(pool), Objects: objectStore, Sessions: sessions, MaxRequestBytes: cfg.MaxRequestBytes, MaxPhotoBytes: cfg.MaxPhotoBytes, Location: location, PageMeta: pageMeta, System: system}
	events := handlers.Events{Store: dbgen.New(pool), DB: pool, PageMeta: pageMeta, Location: location, Sessions: sessions, System: system}
	announcements := handlers.Announcements{Store: dbgen.New(pool), DB: pool, PageMeta: pageMeta, Location: location, Sessions: sessions, System: system}
	training := handlers.Training{Store: dbgen.New(pool), PageMeta: pageMeta, Location: location, Sessions: sessions, System: system}
	members := handlers.Members{Store: dbgen.New(pool), PageMeta: pageMeta, Location: location, Sessions: sessions, System: system}
	profile := handlers.Profile{Store: handlers.PostgresProfileStore{Pool: pool}, Objects: objectStore, PageMeta: pageMeta, Location: location, Sessions: sessions, System: system, MaxRequestBytes: cfg.MaxRequestBytes, MaxPhotoBytes: cfg.MaxPhotoBytes, ImageVersion: cfg.ConsentImageVersion, ImageSHA256: cfg.ConsentImageSHA256, ImageURL: cfg.ConsentImageURL}
	news := handlers.News{Store: dbgen.New(pool), PageMeta: pageMeta, Location: location, Sessions: sessions, System: system}
	foundation := handlers.Foundation{PageMeta: pageMeta}
	router := auth.Load(newRouter(pool, sessions, landing, login, registration, auth, dashboard, repair, events, announcements, training, members, profile, news, foundation))
	csrfMiddleware := csrfProtection(csrfKey, system)

	trusted, err := cfg.TrustedProxyCIDRs()
	if err != nil {
		sessionStore.StopCleanup()
		pool.Close()
		return nil, err
	}

	handler := httpx.Chain(
		router,
		httpx.RecoveryMiddleware(logger, http.HandlerFunc(system.InternalError)),
		httpx.RequestIDMiddleware(),
		httpx.TrustedProxyMiddleware(trusted),
		httpx.SecurityHeadersMiddleware(cfg.IsProduction()),
		httpx.AccessLogMiddleware(logger),
		func(next http.Handler) http.Handler { return sessions.LoadAndSave(next) },
		func(next http.Handler) http.Handler { return csrfMiddleware(next) },
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddress(),
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}

	return &Application{
		Config:       cfg,
		Logger:       logger,
		Location:     location,
		Pool:         pool,
		Sessions:     sessions,
		SessionStore: sessionStore,
		ObjectStore:  objectStore,
		Server:       server,
	}, nil
}

func csrfProtection(authKey []byte, system handlers.System) func(http.Handler) http.Handler {
	return csrf.Protect(authKey, csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		system.RequestRejected(w, r)
	})))
}

func (a *Application) Close() {
	if a.SessionStore != nil {
		a.SessionStore.StopCleanup()
	}
	if a.Pool != nil {
		a.Pool.Close()
	}
}

func (a *Application) Run(ctx context.Context) error {
	serverErrors := make(chan error, 1)
	go func() {
		a.Logger.Info("server starting", "address", a.Server.Addr, "version", a.Config.AppVersion)
		err := a.Server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	signalContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-signalContext.Done():
		a.Logger.Info("shutdown requested")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), a.Config.ShutdownTimeout)
	defer cancel()
	if err := a.Server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	if err := <-serverErrors; err != nil {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}
	return nil
}
