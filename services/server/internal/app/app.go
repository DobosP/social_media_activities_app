package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/admin"
	"github.com/DobosP/social_media_activities_app/services/server/internal/booking"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/discovery"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/notifications"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Donations                          donations.Config
	Booking                            booking.Config
	Ops                                ops.HTTPConfig
	SiteRoot                           string
	AccountRetention                   web.AccountRetentionConfig
	ReactUI, CSPEnforce                bool
	PublicURL                          string
	Secret                             string
	IdentityBindingSecret              string
	Auth                               authcore.Config
	Accounts                           accounts.Config
	Media                              media.Config
	MediaStore                         media.Store
	Scanner                            media.Scanner
	DocumentScanner                    media.DocumentScanner
	AllowUserGroups                    bool
	AllowedHosts                       []string
	TrustedProxyCIDRs                  []string
	ProxyHops                          int
	StaticDir                          string
	ThrottleAnonymous                  int
	ThrottleUser                       int
	ThrottleToken                      int
	VideoInlineProcessing              *bool
	SecureSSLRedirect                  bool
	HSTSSeconds                        int64
	HSTSIncludeSubdomains, HSTSPreload bool
	RequestLoggingEnabled              bool
	LogFormat                          string
	LogWriter                          io.Writer
	ErrorReporter                      *ops.ErrorReporter
	LogLevel                           string
	PermissionsPolicy                  string
	MaxRequestBodyBytes                int64
	DataUploadMemoryBytes              int64
}
type App struct {
	DB              *pgxpool.Pool
	Config          Config
	Auth            *authcore.Service
	Accounts        *accounts.Service
	Social          *social.Service
	Media           *media.Service
	Safety          *safety.Service
	Store           *accounts.Store
	Mux             *http.ServeMux
	Catalog         *catalog.Service
	Recommendations *recommendations.Service
	Discovery       *discovery.Service
	Messaging       *messaging.Service
	Donations       *donations.Service
	Booking         *booking.Service
	Notifications   *notifications.Service
	Ops             *ops.Service
	Admin           *admin.Service
	Broker          *chat.Broker
	Live            *chat.Server
	Web             *web.Server
	Static          http.Handler
	proxyNetworks   []netip.Prefix
	rates           requestRates
}

func New(ctx context.Context, db *pgxpool.Pool, config Config, migrate bool) (*App, error) {
	if db == nil || len(config.Secret) < 32 || strings.Contains(config.Secret, "change-me") {
		return nil, errors.New("DATABASE_URL and a strong DJANGO_SECRET_KEY are required")
	}
	if err := normalizeHTTPPolicy(&config); err != nil {
		return nil, err
	}
	cleanHosts := []string{}
	for _, host := range config.AllowedHosts {
		host = strings.TrimSpace(host)
		if host != "" {
			cleanHosts = append(cleanHosts, host)
		}
	}
	config.AllowedHosts = cleanHosts
	for _, rate := range []*int{&config.ThrottleAnonymous, &config.ThrottleUser, &config.ThrottleToken} {
		if *rate < 0 || *rate > 10000 {
			return nil, errors.New("API throttle rates must be between one and 10000 per minute")
		}
	}
	if config.ThrottleAnonymous == 0 {
		config.ThrottleAnonymous = 60
	}
	if config.ThrottleUser == 0 {
		config.ThrottleUser = 240
	}
	if config.ThrottleToken == 0 {
		config.ThrottleToken = 10
	}
	if len(config.AllowedHosts) == 0 {
		public, err := url.Parse(config.PublicURL)
		if err != nil || public.Hostname() == "" {
			return nil, errors.New("canonical public URL required")
		}
		config.AllowedHosts = []string{public.Hostname()}
	}
	if migrate {
		if err := schema.Migrate(ctx, db); err != nil {
			return nil, errors.New("native schema migration failed")
		}
		if err := catalog.New(db).Migrate(ctx); err != nil {
			return nil, errors.New("native reference-data migration failed")
		}
	}
	store := accounts.NewStore(db)
	if migrate {
		if err := store.Migrate(ctx); err != nil {
			return nil, errors.New("native authentication schema migration failed")
		}
	}
	config.Auth.PublicURL = config.PublicURL
	auth, err := authcore.New(config.Auth, store)
	if err != nil {
		return nil, errors.New("native authentication configuration invalid")
	}
	if config.Accounts.IdentityUniquenessEnforced && config.IdentityBindingSecret == "" {
		return nil, errors.New("IDENTITY_BINDING_SECRET is required for identity uniqueness enforcement")
	}
	binding := config.IdentityBindingSecret
	if binding == "" {
		binding = config.Secret
	}
	accountService := accounts.New(db, auth, binding, config.Accounts)
	if migrate {
		if err := accountService.Migrate(ctx); err != nil {
			return nil, errors.New("native age-state migration failed")
		}
	}
	socialService := social.New(db, platform.RecordAudit)
	socialService.Avatar = accounts.Avatar
	socialService.BodyMarkup = web.BodyMarkup
	socialService.AllowUserGroups = config.AllowUserGroups
	socialService.MinorOnboardingEnabled = config.Accounts.AllowMinorOnboarding
	socialService.Cursor = platform.CursorCodec{Key: []byte(config.Secret)}
	if config.MediaStore == nil {
		return nil, errors.New("private media storage is required")
	}
	processor, err := media.NewProcessor(config.Media, config.Scanner, config.DocumentScanner)
	if err != nil {
		return nil, errors.New("native media configuration invalid")
	}
	if err = processor.CheckRuntime(); err != nil {
		return nil, errors.New("native media codec runtime unavailable")
	}
	mediaService := media.NewService(db, processor, config.MediaStore, media.TokenCodec{Key: []byte(config.Secret)}, socialService)
	socialService.ActivityVisual = mediaService.ActivityVisual
	socialService.ActivityVisuals = mediaService.ActivityVisuals
	if migrate {
		if err := media.EnsureSchema(ctx, db); err != nil {
			return nil, errors.New("native media lifecycle migration failed")
		}
		if err := messaging.EnsureSchema(ctx, db); err != nil {
			return nil, errors.New("native messaging migration failed")
		}
	}
	a := &App{DB: db, Config: config, Auth: auth, Accounts: accountService, Social: socialService, Media: mediaService, Store: store, Mux: http.NewServeMux()}
	if config.ProxyHops < 0 || config.ProxyHops > 32 {
		return nil, errors.New("NUM_PROXIES must be between zero and 32")
	}
	for _, cidr := range config.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, errors.New("TRUSTED_PROXY_CIDRS contains an invalid network")
		}
		a.proxyNetworks = append(a.proxyNetworks, prefix.Masked())
	}
	a.Safety = safety.New(db, safety.Config{Accounts: accountService, CanSeeActivity: socialService.CanSeeActivity, CanReadThread: socialService.CanReadThread})
	if migrate {
		if err := a.Safety.Migrate(ctx); err != nil {
			return nil, errors.New("native safety schema migration failed")
		}
	}
	cursor := platform.CursorCodec{Key: []byte(config.Secret)}
	a.Catalog = catalog.New(db)
	a.Catalog.Cursor = cursor
	a.Catalog.PlaceVisual = mediaService.PlaceVisual
	a.Catalog.PlaceVisuals = mediaService.PlaceVisuals
	a.Recommendations = recommendations.New(db, a.Catalog, socialService)
	a.Recommendations.Cursor = cursor
	a.Social.AfterActivitySave = a.Recommendations.RecomputeEmbeddingTx
	a.Discovery = discovery.New(db, a.Catalog, socialService, a.Recommendations)
	a.Discovery.Cursor = cursor
	a.Messaging = messaging.New(db, cursor)
	a.Donations = donations.New(db, config.Donations)
	a.Booking = booking.NewConfigured(db, config.Booking)
	a.Notifications = notifications.New(db, cursor)
	a.Ops = ops.NewService(db, config.Ops)
	a.Admin = admin.New(db, a.Catalog, a.Social, a.Safety, a.Media)
	a.Broker = chat.NewBroker(db)
	plain, err := chat.PlainAdapter(a.Broker, chat.PlainCallbacks{
		Authorize: func(ctx context.Context, actor platform.Actor, id int64) (bool, error) {
			return a.Social.CanReadThread(ctx, a.DB, actor, id)
		},
		Write: func(ctx context.Context, actor platform.Actor, id int64, body string, reply *int64) error {
			kind, owner, err := a.Social.ThreadOwner(ctx, actor, id)
			if err != nil {
				return err
			}
			_, err = a.Social.WritePost(ctx, actor, kind, owner, social.PostInput{Body: body, ReplyTo: reply}, false)
			return err
		},
		Typing: a.Social.TypingIdentity, Post: a.Social.LivePost,
		Attachments: func(ctx context.Context, actor platform.Actor, post int64) ([]any, error) {
			items, err := a.Media.ForPosts(ctx, actor, []int64{post})
			if err != nil {
				return nil, err
			}
			result := []any{}
			for _, item := range items[post] {
				result = append(result, map[string]any{"id": item.ID, "kind": item.Kind, "url": item.URL, "thumb_url": item.ThumbURL, "poster_url": item.PosterURL, "processing": item.Processing, "failed": item.Failed, "blocked": item.Blocked, "expired": item.Expired, "filename": item.OriginalFilename, "expires_at": item.ExpiresAt})
			}
			return result, nil
		},
	})
	if err != nil {
		return nil, errors.New("native chat adapter invalid")
	}
	chatConfig := chat.DefaultConfig()
	publicURL, _ := url.Parse(config.PublicURL)
	chatConfig.OriginPatterns = []string{publicURL.Host}
	a.Live, err = chat.NewServer(a.Broker, a.authority, map[string]chat.Adapter{"chat": plain, "messaging": a.Messaging.LiveAdapter()}, chatConfig)
	if err != nil {
		return nil, errors.New("native live transport invalid")
	}
	root := config.SiteRoot
	if root == "" {
		root = "."
	}
	a.Web = web.NewServer(db, auth, accountService, socialService, a.Safety, a.Mux, web.Config{Root: root, PublicURL: config.PublicURL, SPA: config.ReactUI, CSPEnforce: config.CSPEnforce, UploadScratch: config.Media.ScratchDir})
	a.Web.Donations = a.Donations
	a.Web.Booking = a.Booking
	a.Web.Notifications = a.Notifications
	a.Web.Catalog = a.Catalog
	a.Web.Discovery = a.Discovery
	a.Web.Recommendations = a.Recommendations
	a.Web.Messaging = a.Messaging
	a.Web.Media = a.Media
	a.Web.Config.AccountRetention = config.AccountRetention
	if config.StaticDir != "" {
		a.Static, err = web.StaticHandler(config.StaticDir)
		if err != nil {
			return nil, errors.New("native release assets unavailable")
		}
	}
	a.register()
	return a, nil
}

func (a *App) register() {
	a.Auth.Register(a.Mux, "/api/auth")
	a.Accounts.Register(a.Mux)
	a.Safety.Register(a.Mux)
	a.Social.Register(a.Mux)
	a.Media.Register(a.Mux)
	a.Catalog.Register(a.Mux)
	a.Booking.Register(a.Mux)
	a.Donations.Register(a.Mux)
	a.Notifications.Register(a.Mux)
	a.Recommendations.Register(a.Mux)
	a.Discovery.Register(a.Mux)
	a.Messaging.Register(a.Mux)
	a.Ops.Register(a.Mux)
	(admin.HTTP{Service: a.Admin, Auth: a.Auth}).Register(a.Mux)
	a.Live.Register(a.Mux)
	a.Web.Register(a.Mux)
	a.Mux.Handle("GET /login/", a.Auth.LoginPage("Activități locale", "/"))
	a.Mux.Handle("GET /register/", a.Auth.LoginPage("Activități locale", "/"))
	a.Mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.JSON(w, 200, map[string]any{"status": "ok", "runtime": "go"})
	})
	a.Mux.HandleFunc("GET /readyz", a.Ops.Ready)
}

func (a *App) StartLive(ctx context.Context) {
	enabled := true
	if a.Config.VideoInlineProcessing != nil {
		enabled = *a.Config.VideoInlineProcessing
	}
	a.Media.SetInlineVideoProcessing(ctx, enabled)
	go a.Broker.Run(ctx)
}

func (a *App) StopBackground(ctx context.Context) error {
	return a.Media.StopInlineVideoProcessing(ctx)
}

// Each socket callback reloads the actual captured credential. The HTTP actor
// context is deliberately not used after the initial handshake.
func (a *App) authority(ctx context.Context, r *http.Request) (platform.Actor, error) {
	r = r.WithContext(ctx)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "token ") {
		return a.Accounts.AuthenticateToken(r)
	}
	user, err := a.Auth.Authenticate(r)
	if err != nil {
		return platform.Actor{}, platform.ErrForbidden
	}
	actor, err := a.Store.Actor(ctx, user.ID)
	if err != nil || !actor.IsActive {
		return platform.Actor{}, platform.ErrForbidden
	}
	return actor, nil
}

func (a *App) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Social-Runtime", "go")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	if !allowedHost(r.Host, a.Config.AllowedHosts) {
		platform.Error(w, 400, "Invalid host.")
		return
	}
	if len(r.URL.EscapedPath()) > 2048 || len(r.URL.RawQuery) > 8192 {
		platform.Error(w, 414, "Request too large.")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/static/") && a.Static != nil {
		a.Static.ServeHTTP(w, r)
		return
	}
	r = a.forwardedPeer(r)
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		limit := a.Config.MaxRequestBodyBytes
		if limit == 0 {
			limit = 8 << 20
		}
		memory := a.Config.DataUploadMemoryBytes
		if memory == 0 {
			memory = 8 << 20
		}
		limit = min(limit, memory)
		if strings.Contains(r.URL.Path, "/media/") || strings.HasSuffix(r.URL.Path, "/attach/") || classicThreadUpload(r) {
			limit = 82 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	var tokenAuthenticated bool
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "token ") {
		actor, err := a.Accounts.AuthenticateToken(r)
		if err != nil {
			platform.Error(w, 401, "Invalid token.")
			return
		}
		r = platform.WithActor(r, actor)
		tokenAuthenticated = true
	}
	user, err := authcore.User{}, authcore.ErrNotFound
	if !tokenAuthenticated {
		user, err = a.Auth.Authenticate(r)
	}
	if err != nil && !errors.Is(err, authcore.ErrNotFound) {
		platform.Error(w, 503, "Authentication unavailable.")
		return
	}
	if err == nil && !tokenAuthenticated {
		actor, err := a.Store.Actor(r.Context(), user.ID)
		if err != nil {
			platform.Error(w, 403, "Authentication unavailable.")
			return
		}
		r = platform.WithActor(r, actor)
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.URL.Path != "/api/ops/csp-report/" && r.URL.Path != "/api/v1/ops/csp-report/" {
			if r.Header.Get("X-CSRFToken") == "" && r.Header.Get("X-CSRF-Token") == "" {
				typeName, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if typeName == "application/x-www-form-urlencoded" {
					if r.ParseForm() != nil {
						platform.Error(w, 400, "Invalid form.")
						return
					}
					r.Header.Set("X-CSRFToken", r.PostForm.Get("csrfmiddlewaretoken"))
				}
				if typeName == "multipart/form-data" {
					if token, err := multipartCSRF(r); err == nil {
						r.Header.Set("X-CSRFToken", token)
					}
				}
			}
			if a.Auth.CheckCSRF(r) != nil {
				platform.Error(w, 403, "CSRF verification failed.")
				return
			}
		}
	}
	if !a.admitAPI(w, r) {
		return
	}
	a.Mux.ServeHTTP(w, r)
}

func classicThreadUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	typeName, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typeName != "multipart/form-data" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 4 || (parts[0] != "activities" && parts[0] != "groups") || parts[2] != "post" || parts[3] != "" {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && id > 0
}

// Native media owns streaming and scratch files. Peek only at the bounded form
// prefix to authenticate a classic HTML upload, then restore every consumed byte.
// The rendered forms always place CSRF before any file; API clients use the header.
func multipartCSRF(r *http.Request) (string, error) {
	_, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || parameters["boundary"] == "" {
		return "", authcore.ErrCSRF
	}
	var prefix bytes.Buffer
	reader := multipart.NewReader(io.TeeReader(io.LimitReader(r.Body, 65536), &prefix), parameters["boundary"])
	original := r.Body
	defer func() {
		r.Body = &restoredBody{Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), original), Closer: original}
	}()
	for i := 0; i < 16; i++ {
		part, err := reader.NextPart()
		if err != nil || part.FileName() != "" {
			return "", authcore.ErrCSRF
		}
		if part.FormName() == "csrfmiddlewaretoken" {
			raw, err := io.ReadAll(io.LimitReader(part, 129))
			if err != nil || len(raw) != 52 {
				return "", authcore.ErrCSRF
			}
			return string(raw), nil
		}
		if _, err = io.Copy(io.Discard, io.LimitReader(part, 8193)); err != nil {
			return "", authcore.ErrCSRF
		}
	}
	return "", authcore.ErrCSRF
}

type restoredBody struct {
	io.Reader
	io.Closer
}

func allowedHost(raw string, hosts []string) bool {
	if strings.ContainsAny(raw, "/\\@\r\n \t") {
		return false
	}
	host := raw
	if parsed, _, err := net.SplitHostPort(raw); err == nil {
		host = parsed
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, allowed := range hosts {
		allowed = strings.ToLower(allowed)
		if allowed == "*" || host == allowed {
			return true
		}
		if strings.HasPrefix(allowed, ".") && (host == allowed[1:] || strings.HasSuffix(host, allowed)) {
			return true
		}
	}
	return false
}
