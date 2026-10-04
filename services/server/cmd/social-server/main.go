package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/app"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

type cliOptions struct {
	Listen, MediaDir, Scratch, StaticDir, SiteRoot string
	Job, JobOptions                                string
	Migrate, MigrateOnly, Due, Dev                 bool
	DevContainer                                   bool
	MediaDirExplicit                               bool
	ReminderWithinHours                            int
}

func parseCLI(args []string, output io.Writer) (cliOptions, error) {
	var o cliOptions
	f := flag.NewFlagSet("social-server", flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&o.Listen, "listen", "127.0.0.1:8000", "HTTP listen address")
	f.BoolVar(&o.Migrate, "migrate", false, "bootstrap native database contracts before running")
	f.BoolVar(&o.MigrateOnly, "migrate-only", false, "bootstrap native database contracts and exit")
	f.BoolVar(&o.Dev, "dev", false, "allow local private storage on a loopback development origin")
	f.BoolVar(&o.DevContainer, "dev-container", false, "explicit container dev listener exception; requires --dev, loopback origin and loopback-only published ports")
	f.StringVar(&o.MediaDir, "media-dir", "var/media", "private local development blob directory")
	f.StringVar(&o.Scratch, "media-scratch", "var/media-work", "private bounded codec scratch")
	f.StringVar(&o.StaticDir, "static-dir", "static", "frontend and static assets")
	f.StringVar(&o.SiteRoot, "site-root", ".", "immutable native HTML/static/locale release root")
	f.StringVar(&o.Job, "job", "", "run one registered native job or operator command and exit")
	f.BoolVar(&o.Due, "due", false, "run the native due-job registry once and exit")
	f.IntVar(&o.ReminderWithinHours, "reminder-within-hours", 0, "override activity-reminder lookahead for --due (1..8760 hours)")
	f.StringVar(&o.JobOptions, "job-options", "", "bounded JSON options object from a regular file, or - for stdin")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, flag.ErrHelp
		}
		return o, errors.New("invalid command-line flags")
	}
	if f.NArg() != 0 || o.Job != "" && o.Due || o.MigrateOnly && (o.Job != "" || o.Due) || o.JobOptions != "" && o.Job == "" {
		return o, errors.New("incompatible command-line flags")
	}
	if o.DevContainer && !o.Dev {
		return o, errors.New("--dev-container requires --dev")
	}
	var validationErr error
	f.Visit(func(value *flag.Flag) {
		if value.Name == "media-dir" {
			o.MediaDirExplicit = true
		}
		if value.Name == "reminder-within-hours" && (!o.Due || o.ReminderWithinHours < 1 || o.ReminderWithinHours > 24*365) {
			validationErr = errors.New("--reminder-within-hours requires --due and bounded positive hours")
		}
	})
	return o, validationErr
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, func(name string) bool { _, exists := os.LookupEnv(name); return exists }); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, get environment, stdin io.Reader, stdout, stderr io.Writer, presence ...environmentPresence) error {
	o, err := parseCLI(args, io.Discard)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = parseCLI([]string{"-help"}, stdout)
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = jobInvocation(o.Job, nil); err != nil {
		return err
	}
	if (o.Job == "createsuperuser" || o.Job == "create_superuser") && o.JobOptions != "-" {
		return errors.New("--job-options must use secret stdin for administrator bootstrap")
	}
	options, err := readJobOptions(o.JobOptions, stdin)
	if err != nil {
		return err
	}
	call, err := jobInvocation(o.Job, options)
	if err != nil {
		return err
	}
	poolCfg, err := databaseConfig(get, presence...)
	if err != nil {
		return err
	}
	var runtime runtimeConfig
	if !o.MigrateOnly {
		runtime, err = configuration(get, o, presence...)
		if err != nil {
			return err
		}
	}
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return errors.New("DATABASE_URL is unavailable")
	}
	defer db.Close()
	boot, bootCancel := context.WithTimeout(ctx, 30*time.Second)
	if o.Migrate || o.MigrateOnly {
		err = migrateDatabase(boot, db)
	}
	bootCancel()
	if err != nil {
		return errors.New("native database migration failed")
	}
	if o.MigrateOnly {
		return json.NewEncoder(stdout).Encode(map[string]string{"status": "migrated"})
	}
	if err = preparePrivateDirectory(runtime.App.Media.ScratchDir); err != nil {
		return errors.New("private media scratch unavailable")
	}
	if err = os.Setenv("TMPDIR", runtime.App.Media.ScratchDir); err != nil {
		return errors.New("private multipart scratch unavailable")
	}
	store, err := storageFromEnvironment(get, runtime.MediaDirectory, runtime.Development, presence...)
	if err != nil {
		return err
	}
	if closer, ok := store.(io.Closer); ok {
		defer closer.Close()
	}
	runtime.App.MediaStore = store
	scanner, documents, err := scannersFromEnvironment(get, presence...)
	if err != nil {
		return err
	}
	runtime.App.Scanner, runtime.App.DocumentScanner = scanner, documents
	boot, bootCancel = context.WithTimeout(ctx, 30*time.Second)
	handler, err := app.New(boot, db, runtime.App, false)
	bootCancel()
	if err != nil {
		return err
	}
	runtime.apply(handler)
	jobConfig := runtime.Jobs
	if o.ReminderWithinHours > 0 {
		jobConfig.ReminderHours = o.ReminderWithinHours
	}
	jobConfig.Social, jobConfig.Media, jobConfig.Safety, jobConfig.Accounts = handler.Social, handler.Media, handler.Safety, handler.Accounts
	jobConfig.DeleteBlob = store.Delete
	runner := jobs.New(db, jobConfig)
	runner.Queue.BackoffBase, runner.Queue.BackoffMax = runtime.BackoffBase, runtime.BackoffMax
	operatorConfig := runtime.Commands
	operatorConfig.Recommendations, operatorConfig.Storage = handler.Recommendations, store
	operatorConfig.DecodeProfileHash = handler.Media.ProfileHash
	operatorConfig.BootstrapAdministrator = handler.Accounts.BootstrapAdministrator
	operatorConfig.SeedAccount = handler.Accounts.EnsureDemoAccount
	operatorConfig.SeedConsent = handler.Accounts.DemoLinkAndConsent
	operatorConfig.SeedComplete = func(ctx context.Context, actor platform.Actor, id int64) error {
		return handler.Social.CompleteDemoActivity(ctx, actor, id, runtime.Development)
	}
	operatorConfig.SeedApproveVenue = func(ctx context.Context, actor platform.Actor, id int64) error {
		return handler.Social.ApproveDemoVenue(ctx, actor, id, runtime.Development)
	}
	operatorConfig.Messaging, operatorConfig.Media, operatorConfig.Scratch = handler.Messaging, handler.Media, runtime.App.Media.ScratchDir
	if err = commands.Install(runner, operatorConfig); err != nil {
		return errors.New("native operator command registry invalid")
	}
	if o.Due {
		result, runErr := runner.RunDue(ctx)
		if err = json.NewEncoder(stdout).Encode(result); err != nil {
			return errors.New("job result output unavailable")
		}
		if runErr != nil {
			return errors.New("native due jobs failed")
		}
		return nil
	}
	if o.Job != "" {
		jobCtx, cancel := context.WithTimeout(ctx, jobConfig.JobTimeout)
		defer cancel()
		result, runErr := call.run(jobCtx, runner)
		if runErr != nil || jobCtx.Err() != nil {
			return errors.New("native job failed")
		}
		if err = json.NewEncoder(stdout).Encode(result); err != nil {
			return errors.New("job result output unavailable")
		}
		return nil
	}
	// One-shot jobs do not reserve the LISTEN connection or start a scheduler.
	serverCtx, cancelServer := context.WithCancel(context.Background())
	handler.StartLive(serverCtx)
	defer func() {
		cancelServer()
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = handler.StopBackground(stopCtx)
	}()
	server := &http.Server{Addr: o.Listen, Handler: handler, BaseContext: func(net.Listener) context.Context { return serverCtx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	finished := make(chan struct{})
	defer close(finished)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
		case <-finished:
			return
		}
		handler.Ops.MarkDraining()
		drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(drain)
	}()
	_, _ = io.WriteString(stderr, "native Go social server starting\n")
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP server unavailable")
	}
	if ctx.Err() != nil {
		<-shutdownDone
	}
	return nil
}

// Schema installation is independent of the serving/codec runtime, allowing a
// one-shot migration without booting sockets or workers.
func migrateDatabase(ctx context.Context, db *pgxpool.Pool) error {
	steps := []func(context.Context) error{
		func(ctx context.Context) error { return schema.Migrate(ctx, db) },
		catalog.New(db).Migrate, accounts.NewStore(db).Migrate,
		accounts.New(db, nil, "", accounts.Config{}).Migrate,
		func(ctx context.Context) error { return media.EnsureSchema(ctx, db) },
		func(ctx context.Context) error { return messaging.EnsureSchema(ctx, db) },
		safety.New(db, safety.Config{}).Migrate,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

func preparePrivateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("private directory must be absolute")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("private directory permissions invalid")
	}
	return nil
}
