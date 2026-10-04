package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/app"
	"github.com/DobosP/social_media_activities_app/services/server/internal/booking"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

type environment func(string) string
type environmentPresence func(string) bool

type decoder struct {
	get     environment
	present environmentPresence
	err     error
}

func newDecoder(get environment, presence ...environmentPresence) decoder {
	d := decoder{get: get}
	if len(presence) > 0 {
		d.present = presence[0]
	}
	return d
}
func (d *decoder) isSet(name string) bool {
	return d.get(name) != "" || d.present != nil && d.present(name)
}

func (d *decoder) invalid(name string) {
	if d.err == nil {
		d.err = errors.New(name + " is invalid or unsupported by the native runtime")
	}
}
func (d *decoder) value(name, fallback string) string {
	if v := d.get(name); v != "" {
		return v
	}
	if d.isSet(name) {
		return ""
	}
	return fallback
}
func (d *decoder) boolean(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(d.get(name)))
	switch raw {
	case "":
		if d.isSet(name) {
			d.invalid(name)
		}
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		d.invalid(name)
		return fallback
	}
}
func (d *decoder) integer(name string, fallback, floor, ceiling int) int {
	raw := d.get(name)
	if raw == "" {
		if d.isSet(name) {
			d.invalid(name)
		}
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < floor || n > ceiling {
		d.invalid(name)
		return fallback
	}
	return n
}
func (d *decoder) list(name string, fallback []string) []string {
	raw := d.get(name)
	if raw == "" {
		if d.isSet(name) {
			return []string{}
		}
		return append([]string{}, fallback...)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] || len(item) > 2048 || strings.ContainsAny(item, "\x00\r\n") {
			d.invalid(name)
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
func (d *decoder) stringMap(name string) map[string]string {
	out := map[string]string{}
	raw := d.get(name)
	if raw == "" {
		if d.isSet(name) {
			d.invalid(name)
		}
		return out
	}
	if len(raw) > 64<<10 || decodeJSONObject([]byte(raw), &out) != nil {
		d.invalid(name)
		return map[string]string{}
	}
	for key, value := range out {
		if strings.TrimSpace(key) == "" || value == "" {
			d.invalid(name)
		}
	}
	return out
}
func (d *decoder) fixedInt(name string, expected int) {
	if d.integer(name, expected, -1000000000, 1000000000) != expected {
		d.invalid(name)
	}
}
func (d *decoder) fixedBool(name string, expected bool) {
	if d.boolean(name, expected) != expected {
		d.invalid(name)
	}
}
func (d *decoder) fixedString(name, expected string) {
	if raw := d.get(name); d.isSet(name) && raw != expected {
		d.invalid(name)
	}
}
func (d *decoder) fixedList(name string, expected []string) {
	actual := d.list(name, expected)
	want := append([]string{}, expected...)
	sort.Strings(actual)
	sort.Strings(want)
	if strings.Join(actual, "\x00") != strings.Join(want, "\x00") {
		d.invalid(name)
	}
}

func (d *decoder) minuteRate(name string, fallback int) int {
	if d.get(name) == "" {
		if d.isSet(name) {
			d.invalid(name)
		}
		return fallback
	}
	parts := strings.Split(d.get(name), "/")
	if len(parts) != 2 || (parts[1] != "min" && parts[1] != "minute" && parts[1] != "minutes" && parts[1] != "m") {
		d.invalid(name)
		return fallback
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 || n > 10000 {
		d.invalid(name)
		return fallback
	}
	return n
}

type runtimeConfig struct {
	App                                 app.Config
	Jobs                                jobs.Config
	Development                         bool
	MediaDirectory                      string
	Sentiment                           social.SentimentConfig
	Community                           social.CommunityConfig
	Connections                         map[string]bool
	Presign                             bool
	BackoffBase, BackoffMax             time.Duration
	Web                                 web.Config
	Commands                            commands.Config
	CatalogPolicy                       catalog.Policy
	SocialPolicy                        social.PolicyConfig
	MediaPolicy                         media.PolicyConfig
	MessagingPolicy                     messaging.Policy
	SavedSearchMax, DeferredMaxAttempts int
	UnsafeReportCooldown                time.Duration
	ErrorReporting                      ops.ErrorReporterConfig
	RequireSharedState                  bool
	Rates                               map[string]configuredRate
}

func (c runtimeConfig) apply(a *app.App) error {
	if err := c.applyRates(a); err != nil {
		return err
	}
	if err := c.SocialPolicy.Validate(); err != nil {
		return err
	}
	if err := c.CatalogPolicy.Validate(); err != nil {
		return err
	}
	if err := a.Social.ConfigurePolicy(c.SocialPolicy); err != nil {
		return err
	}
	if err := a.Media.ConfigurePolicy(c.MediaPolicy); err != nil {
		return err
	}
	if err := a.Messaging.ConfigurePolicy(c.MessagingPolicy); err != nil {
		return err
	}
	a.Catalog.Policy = c.CatalogPolicy
	a.Recommendations.MaxSavedSearches = c.SavedSearchMax
	a.Safety.Config.UnsafeReportCooldown = c.UnsafeReportCooldown
	a.Media.ClosureThreshold, a.Media.ClosureDecay = c.CatalogPolicy.ClosureReportThreshold, c.CatalogPolicy.ClosureReportDecay
	a.Social.Sentiment = c.Sentiment
	a.Social.CommunityPolicy = c.Community
	a.Social.ConnectionCohorts = map[string]bool{}
	for cohort, enabled := range c.Connections {
		a.Social.ConnectionCohorts[cohort] = enabled
	}
	a.Media.Presign = c.Presign
	a.Web.Config.IndexNowKey = c.Web.IndexNowKey
	a.Web.Config.SnapshotDir = c.Web.SnapshotDir
	a.Web.Config.SiteSameAs = c.Web.SiteSameAs
	a.Web.Config.SiteAreaServed = c.Web.SiteAreaServed
	a.Web.Config.SiteContactEmail = c.Web.SiteContactEmail
	a.Web.Config.SiteName = c.Web.SiteName
	a.Web.Config.SiteGoogleVerification = c.Web.SiteGoogleVerification
	a.Web.Config.SiteBingVerification = c.Web.SiteBingVerification
	return nil
}

func configuration(get environment, o cliOptions, presence ...environmentPresence) (runtimeConfig, error) {
	d := newDecoder(get, presence...)
	var out runtimeConfig
	origin, err := canonicalOrigin(get("SITE_BASE_URL"), o.Listen)
	if err != nil {
		return out, err
	}
	debug := d.boolean("DJANGO_DEBUG", false)
	out.Development = o.Dev || debug
	if o.DevContainer && !o.Dev {
		return out, errors.New("--dev-container requires --dev")
	}
	out.MediaDirectory = o.MediaDir
	if !o.MediaDirExplicit && get("MEDIA_ROOT") != "" {
		out.MediaDirectory = get("MEDIA_ROOT")
	}
	u, _ := url.Parse(origin)
	listenHost, listenPort, listenErr := net.SplitHostPort(o.Listen)
	portNumber, portErr := strconv.Atoi(listenPort)
	if listenErr != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return out, errors.New("--listen is invalid")
	}
	if out.Development && !o.DevContainer && !loopbackHost(listenHost) {
		return out, errors.New("--listen must bind loopback in development mode")
	}
	if out.Development && !loopbackHost(u.Hostname()) {
		d.invalid("SITE_BASE_URL")
	}
	secret := get("DJANGO_SECRET_KEY")
	if !strongSecret(secret) {
		d.invalid("DJANGO_SECRET_KEY")
	}
	binding := get("IDENTITY_BINDING_SECRET")
	if binding != "" && !strongSecret(binding) {
		d.invalid("IDENTITY_BINDING_SECRET")
	}
	unique := d.boolean("IDENTITY_UNIQUENESS_ENFORCED", false)
	if unique && (binding == "" || binding == secret) {
		d.invalid("IDENTITY_BINDING_SECRET")
	}
	trusted := d.stringMap("EUDI_TRUSTED_ISSUERS")
	if len(trusted) > 1000 {
		d.invalid("EUDI_TRUSTED_ISSUERS")
	}
	for issuer, raw := range trusted {
		block, rest := pem.Decode([]byte(raw))
		if issuer == "" || len(issuer) > 512 || strings.ContainsAny(issuer, "\r\n\x00") || block == nil || strings.TrimSpace(string(rest)) != "" {
			d.invalid("EUDI_TRUSTED_ISSUERS")
			continue
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		public, ok := key.(*ecdsa.PublicKey)
		if err != nil || !ok || public.Curve != elliptic.P256() || !public.Curve.IsOnCurve(public.X, public.Y) {
			d.invalid("EUDI_TRUSTED_ISSUERS")
		}
	}
	// The native identity flow is a reviewed EUDI seam. Arbitrary Python classes
	// cannot be imported by this process, even on an operator path.
	provider := get("IDENTITY_PROVIDER")
	if provider != "" && provider != "eudi" && provider != "apps.accounts.identity.providers.eudi.EUDIWalletProvider" {
		d.invalid("IDENTITY_PROVIDER")
	}
	if provider != "" && !out.Development && len(trusted) == 0 {
		d.invalid("EUDI_TRUSTED_ISSUERS")
	}
	d.fixedBool("IDENTITY_ALLOW_DEV_PROVIDER", false)
	d.fixedBool("EUDI_SANDBOX", false)
	d.fixedString("EUDI_SANDBOX_ISSUER_KEY_PEM", "")
	auth := authcore.Config{GoogleClientID: get("GOOGLE_OAUTH_CLIENT_ID"), GoogleClientSecret: get("GOOGLE_OAUTH_CLIENT_SECRET"), FacebookClientID: get("FACEBOOK_OAUTH_CLIENT_ID"), FacebookClientSecret: get("FACEBOOK_OAUTH_CLIENT_SECRET"), FacebookAPIVersion: get("FACEBOOK_API_VERSION")}
	for _, pair := range [][2]string{{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET"}, {"FACEBOOK_OAUTH_CLIENT_ID", "FACEBOOK_OAUTH_CLIENT_SECRET"}} {
		if (get(pair[0]) == "") != (get(pair[1]) == "") {
			d.invalid(pair[0])
		}
	}
	hosts := d.list("DJANGO_ALLOWED_HOSTS", []string{u.Hostname()})
	if len(hosts) == 0 {
		d.invalid("DJANGO_ALLOWED_HOSTS")
	}
	for _, host := range hosts {
		if !validHost(host) {
			d.invalid("DJANGO_ALLOWED_HOSTS")
		}
	}
	proxyCIDRs := d.list("TRUSTED_PROXY_CIDRS", nil)
	for _, cidr := range proxyCIDRs {
		_, network, e := net.ParseCIDR(cidr)
		if e != nil || network == nil {
			d.invalid("TRUSTED_PROXY_CIDRS")
		}
	}
	hops := d.integer("NUM_PROXIES", 1, 0, 16)
	if len(proxyCIDRs) > 0 && hops == 0 {
		d.invalid("NUM_PROXIES")
	}
	scratch, err := filepath.Abs(o.Scratch)
	if err != nil {
		return out, errors.New("--media-scratch is invalid")
	}
	siteRoot, err := filepath.Abs(o.SiteRoot)
	if err != nil {
		return out, errors.New("--site-root is invalid")
	}
	static, err := filepath.Abs(o.StaticDir)
	if err != nil {
		return out, errors.New("--static-dir is invalid")
	}
	mc := media.DefaultConfig(scratch)
	mc.VideoEnabled = d.boolean("MEDIA_VIDEO_ENABLED", true)
	mc.ImageFormat = strings.ToUpper(d.value("MEDIA_IMAGE_OUTPUT_FORMAT", "AVIF"))
	if mc.ImageFormat != "AVIF" && mc.ImageFormat != "WEBP" {
		d.invalid("MEDIA_IMAGE_OUTPUT_FORMAT")
	}
	mc.ImageMaxBytes = int64(d.integer("MEDIA_MAX_UPLOAD_BYTES", 5<<20, 1, 5<<20))
	mc.ImageMaxPixels = int64(d.integer("MEDIA_MAX_IMAGE_PIXELS", 30000000, 1, 30000000))
	mc.ImageMaxSide = d.integer("MEDIA_MAX_DIMENSION", 2048, 1, 2048)
	mc.ThumbnailSide = d.integer("MEDIA_THUMB_DIMENSION", 800, 1, 800)
	mc.VideoMaxBytes = int64(d.integer("MEDIA_VIDEO_MAX_UPLOAD_BYTES", 80<<20, 1, 80<<20))
	mc.VideoMaxSeconds = float64(d.integer("MEDIA_VIDEO_MAX_DURATION_SECONDS", 90, 1, 90))
	mc.VideoSourceSide = d.integer("MEDIA_VIDEO_MAX_SOURCE_SIDE", 3840, 1, 3840)
	mc.VideoTargetSide = d.integer("MEDIA_VIDEO_TARGET_MAX_SIDE", 1280, 2, 1280)
	mc.Threads = d.integer("MEDIA_VIDEO_THREADS", 2, 1, 2)
	mc.CommandTimeout = time.Duration(d.integer("MEDIA_VIDEO_FFMPEG_TIMEOUT", 600, 1, 600)) * time.Second
	mc.ProbeTimeout = time.Duration(d.integer("MEDIA_VIDEO_PROBE_TIMEOUT", 60, 1, 60)) * time.Second
	accountsConfig := accounts.Config{EUDIClientID: d.value("EUDI_CLIENT_ID", "social-activities-app"), TrustedIssuers: trusted, AllowMinorOnboarding: d.boolean("ALLOW_MINOR_ONBOARDING", false), IdentityUniquenessEnforced: unique}
	if accountsConfig.EUDIClientID == "" {
		d.invalid("EUDI_CLIENT_ID")
	}
	accountsConfig.DemoEnabled = out.Development
	accountsConfig.GuardianInviteTTL = time.Duration(d.integer("GUARDIAN_INVITE_TTL_DAYS", 7, 1, 365)) * 24 * time.Hour
	accountsConfig.ConsentValidity = time.Duration(d.integer("CONSENT_VALIDITY_DAYS", 365, 1, 365)) * 24 * time.Hour
	tokenDays := d.integer("API_TOKEN_MAX_AGE_DAYS", 90, 1, 365)
	accountsConfig.APITokenTTL = time.Duration(tokenDays) * 24 * time.Hour
	out.App = app.Config{PublicURL: origin, Secret: secret, IdentityBindingSecret: binding, Auth: auth, Accounts: accountsConfig, Media: mc, AllowUserGroups: d.boolean("GROUPS_ALLOW_USER_CREATED", false), AllowedHosts: hosts, TrustedProxyCIDRs: proxyCIDRs, ProxyHops: hops, SiteRoot: siteRoot, StaticDir: static, ReactUI: d.boolean("SOCIAL_REACT_UI", false), CSPEnforce: d.boolean("DJANGO_CSP_ENFORCE", false)}
	out.App.ThrottleAnonymous = d.minuteRate("DRF_THROTTLE_ANON", 60)
	out.App.ThrottleUser = d.minuteRate("DRF_THROTTLE_USER", 240)
	out.App.ThrottleToken = d.minuteRate("DRF_THROTTLE_TOKEN_OBTAIN", 10)
	inlineVideo := d.boolean("MEDIA_VIDEO_INLINE_PROCESSING", true)
	out.App.VideoInlineProcessing = &inlineVideo
	out.App.SecureSSLRedirect = d.boolean("DJANGO_SECURE_SSL_REDIRECT", !out.Development)
	hsts := 31536000
	logFormat := "json"
	if out.Development {
		hsts = 0
		logFormat = "plain"
	}
	out.App.HSTSSeconds = int64(d.integer("DJANGO_HSTS_SECONDS", hsts, 0, 315360000))
	out.App.HSTSIncludeSubdomains, out.App.HSTSPreload = !out.Development, !out.Development
	out.App.RequestLoggingEnabled = d.boolean("REQUEST_LOGGING_ENABLED", !out.Development)
	out.App.LogFormat = d.value("LOG_FORMAT", logFormat)
	if out.App.LogFormat != "json" && out.App.LogFormat != "plain" {
		d.invalid("LOG_FORMAT")
	}
	if raw := get("ALLOW_USER_GROUPS"); raw != "" && d.boolean("ALLOW_USER_GROUPS", false) != out.App.AllowUserGroups {
		d.invalid("ALLOW_USER_GROUPS")
	}
	out.Presign = d.boolean("MEDIA_REDIRECT_TO_PRESIGNED", false)
	out.App.Booking = booking.Config{DemoBaseURL: get("BOOKING_DEMO_BASE_URL"), DemoAPIKey: get("BOOKING_DEMO_API_KEY")}
	if out.App.Booking.DemoBaseURL != "" && !safeHTTPS(out.App.Booking.DemoBaseURL, false) {
		d.invalid("BOOKING_DEMO_BASE_URL")
	}
	for slug, path := range d.stringMap("BOOKING_PROVIDERS") {
		known := (slug == "demo_rest" && (path == "demo_rest" || path == "apps.booking.providers.demo_rest.DemoRestProvider")) || (slug == "deeplink" && (path == "deeplink" || path == "apps.booking.providers.deeplink.DeepLinkProvider"))
		if !known {
			d.invalid("BOOKING_PROVIDERS")
		}
	}
	donor := d.value("DONATIONS_PROVIDER", "apps.donations.providers.DeepLinkProvider")
	switch donor {
	case "apps.donations.providers.DeepLinkProvider", "deeplink":
		donor = "deeplink"
	case "apps.donations.providers.StripePaymentProvider", "stripe":
		donor = "stripe"
	case "apps.donations.providers.DevPaymentProvider", "dev":
		donor = "dev"
		if !out.Development {
			d.invalid("DONATIONS_PROVIDER")
		}
	default:
		d.invalid("DONATIONS_PROVIDER")
	}
	out.App.Donations = donations.Config{Provider: donor, CheckoutURL: get("DONATIONS_CHECKOUT_BASE_URL"), StripeSecret: get("STRIPE_SECRET_KEY"), StripeWebhookSecret: get("STRIPE_WEBHOOK_SECRET"), WebhookSecret: get("DONATIONS_WEBHOOK_SECRET"), SuccessURL: get("DONATIONS_SUCCESS_URL"), CancelURL: get("DONATIONS_CANCEL_URL")}
	for _, name := range []string{"DONATIONS_CHECKOUT_BASE_URL", "DONATIONS_SUCCESS_URL", "DONATIONS_CANCEL_URL"} {
		if get(name) != "" && !safeHTTPS(get(name), false) {
			d.invalid(name)
		}
	}
	for _, name := range []string{"DONATIONS_WEBHOOK_SECRET", "STRIPE_WEBHOOK_SECRET", "METRICS_TOKEN"} {
		if raw := get(name); raw != "" && !strongSecret(raw) {
			d.invalid(name)
		}
	}
	if donor == "stripe" {
		for _, name := range []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "DONATIONS_SUCCESS_URL", "DONATIONS_CANCEL_URL"} {
			if get(name) == "" {
				d.invalid(name)
			}
		}
	}
	out.App.Ops = ops.HTTPConfig{Version: d.value("APP_VERSION", "0.1.0"), MetricsToken: get("METRICS_TOKEN")}
	out.Jobs = jobs.DefaultConfig()
	jc := &out.Jobs
	jc.MessagingRetentionDays = d.integer("MESSAGING_RETENTION_DAYS", 0, 0, 36500)
	jc.NotificationRetentionDays = d.integer("NOTIFICATION_RETENTION_DAYS", 180, 0, 36500)
	jc.NotificationBatch = d.integer("NOTIFICATION_RETENTION_BATCH", 1000, 1, 10000)
	jc.APITokenMaxAgeDays = tokenDays
	jc.ReverifyReminderDays = d.integer("REVERIFY_REMINDER_DAYS", 14, 1, 365)
	jc.ConsentReminderDays = d.integer("CONSENT_RENEWAL_REMINDER_DAYS", 14, 1, 365)
	jc.SweepBatch = d.integer("REVERIFY_SWEEP_BATCH", 1000, 1, 10000)
	if d.integer("CONSENT_SWEEP_BATCH", 1000, 1, 10000) != jc.SweepBatch {
		d.invalid("CONSENT_SWEEP_BATCH")
	}
	jc.RoeduSyncEnabled = d.boolean("ROEDU_SYNC_ENABLED", false)
	jc.RoeduCity = d.value("ROEDU_SYNC_CITY", "Cluj-Napoca")
	if len(jc.RoeduCity) > 160 || strings.TrimSpace(jc.RoeduCity) != jc.RoeduCity || strings.ContainsAny(jc.RoeduCity, "\x00\r\n") {
		d.invalid("ROEDU_SYNC_CITY")
	}
	apiURL := get("ROEDU_API_URL")
	if apiURL != "" && !safeHTTPS(apiURL, true) {
		d.invalid("ROEDU_API_URL")
	}
	if jc.RoeduSyncEnabled && apiURL == "" {
		d.invalid("ROEDU_API_URL")
	}
	if jc.RoeduSyncEnabled && get("ROEDU_API_KEY") == "" {
		d.invalid("ROEDU_API_KEY")
	}
	if apiURL != "" {
		jc.Roedu = &jobs.RoeduClient{BaseURL: apiURL, APIKey: get("ROEDU_API_KEY")}
	}
	jc.IndexNowEnabled = d.boolean("INDEXNOW_ENABLED", false)
	jc.IndexNowKey = get("INDEXNOW_KEY")
	if jc.IndexNowKey != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`).MatchString(jc.IndexNowKey) {
		d.invalid("INDEXNOW_KEY")
	}
	if jc.IndexNowEnabled && (jc.IndexNowKey == "" || u.Scheme != "https") {
		d.invalid("INDEXNOW_ENABLED")
	}
	jc.SiteBaseURL = origin
	jc.AgentSnapshotDir = get("AGENT_SNAPSHOT_DIR")
	if jc.AgentSnapshotDir != "" {
		if !filepath.IsAbs(jc.AgentSnapshotDir) || filepath.Clean(jc.AgentSnapshotDir) == "/" {
			d.invalid("AGENT_SNAPSHOT_DIR")
		}
	}
	commonsURL := d.value("COMMONS_API_URL", "https://commons.wikimedia.org/w/api.php")
	if !safeHTTPS(commonsURL, false) {
		d.invalid("COMMONS_API_URL")
	}
	agent := d.value("INGEST_USER_AGENT", "social-activities-app/0.1 (nonprofit; contact: you@example.org)")
	if len(agent) > 512 || strings.ContainsAny(agent, "\r\n\x00") {
		d.invalid("INGEST_USER_AGENT")
	}
	jc.Commons = &jobs.CommonsClient{APIURL: commonsURL, UserAgent: agent, TempDir: scratch}
	out.Commands = commands.Config{SeedRoot: filepath.Join(siteRoot, "db"), DemoEnabled: out.Development, OverpassURL: d.value("OVERPASS_URL", "https://overpass-api.de/api/interpreter"), DefaultCity: d.value("INGEST_DEFAULT_CITY", "Cluj-Napoca"), Sources: map[string]commands.PlaceSource{}, FetchOverpass: overpassFetch(agent)}
	overturePath := get("OVERTURE_DATA_PATH")
	if strings.ContainsAny(overturePath, "\r\n\x00") {
		d.invalid("OVERTURE_DATA_PATH")
	}
	out.Commands.Sources["overture"] = overtureSource{DefaultPath: overturePath}
	if !safeHTTPS(out.Commands.OverpassURL, false) {
		d.invalid("OVERPASS_URL")
	}
	if len(out.Commands.DefaultCity) > 160 || strings.TrimSpace(out.Commands.DefaultCity) != out.Commands.DefaultCity || strings.ContainsAny(out.Commands.DefaultCity, "\x00\r\n") {
		d.invalid("INGEST_DEFAULT_CITY")
	}
	if jc.Roedu != nil {
		out.Commands.Sources["roedu"] = roeduPlaces{client: jc.Roedu}
	}
	googleEnabled := d.boolean("GOOGLE_PLACES_ENABLED", false)
	if googleEnabled {
		key := get("GOOGLE_PLACES_API_KEY")
		if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
			d.invalid("GOOGLE_PLACES_API_KEY")
		}
		out.Commands.GoogleEnrich = googleEnricher(key, nil)
	}
	wikidataURL := d.value("WIKIDATA_SPARQL_URL", "https://query.wikidata.org/sparql")
	if !safeHTTPS(wikidataURL, false) {
		d.invalid("WIKIDATA_SPARQL_URL")
	}
	out.Commands.WikidataEnrich = wikidataEnricher(wikidataURL, agent, nil)
	heartbeat := get("OPS_HEARTBEAT_URL")
	if heartbeat != "" {
		if !safeHTTPS(heartbeat, false) {
			d.invalid("OPS_HEARTBEAT_URL")
		} else {
			jc.Heartbeat = heartbeatPing(heartbeat, nil)
		}
	}
	out.BackoffBase = time.Duration(d.integer("DEFERRED_TASKS_BACKOFF_BASE", 30, 1, 3600)) * time.Second
	out.BackoffMax = time.Duration(d.integer("DEFERRED_TASKS_MAX_BACKOFF", 3600, 1, 86400)) * time.Second
	if out.BackoffMax < out.BackoffBase {
		d.invalid("DEFERRED_TASKS_MAX_BACKOFF")
	}
	out.App.AccountRetention = web.AccountRetentionConfig{MessagingDays: jc.MessagingRetentionDays, ArrivalHours: 6, AdultPhotoMinimumSeconds: 3600, MinorPhotoMinimumSeconds: 86400}
	out.Sentiment = sentimentConfiguration(&d)
	out.Community = social.CommunityConfig{MinActivities: d.integer("COMMUNITY_MIN_ACTIVITIES", 3, 3, 100000), AnonymousFloor: d.integer("COMMUNITY_K_ANON_FLOOR", 5, 5, 100000), MinDays: d.integer("COMMUNITY_MIN_DAYS", 2, 2, 36500), LookbackDays: d.integer("COMMUNITY_LOOKBACK_DAYS", 180, 1, 36500)}
	out.Connections = map[string]bool{}
	for _, cohort := range d.list("CONNECTIONS_ALLOWED_COHORTS", []string{"adult", "teen", "child"}) {
		if cohort != "adult" && cohort != "teen" && cohort != "child" {
			d.invalid("CONNECTIONS_ALLOWED_COHORTS")
		} else {
			out.Connections[cohort] = true
		}
	}
	out.Web = web.Config{IndexNowKey: jc.IndexNowKey, SnapshotDir: jc.AgentSnapshotDir, SiteAreaServed: d.value("SITE_AREA_SERVED", "Cluj-Napoca"), SiteContactEmail: get("SITE_CONTACT_EMAIL"), SiteSameAs: d.list("SITE_SAMEAS", nil)}
	out.Web.SiteName = d.value("SITE_NAME", "Activities")
	if out.Web.SiteName == "" || len([]rune(out.Web.SiteName)) > 200 || strings.TrimSpace(out.Web.SiteName) != out.Web.SiteName || strings.ContainsAny(out.Web.SiteName, "\r\n\x00") {
		d.invalid("SITE_NAME")
	}
	out.Web.SiteGoogleVerification = get("GOOGLE_SITE_VERIFICATION")
	out.Web.SiteBingVerification = get("BING_SITE_VERIFICATION")
	for _, name := range []string{"GOOGLE_SITE_VERIFICATION", "BING_SITE_VERIFICATION"} {
		if raw := get(name); raw != "" && (len(raw) > 512 || strings.ContainsAny(raw, "\r\n\x00")) {
			d.invalid(name)
		}
	}
	for _, same := range out.Web.SiteSameAs {
		if !safeHTTPS(same, false) {
			d.invalid("SITE_SAMEAS")
		}
	}
	if out.Web.SiteContactEmail != "" {
		mailbox, e := mail.ParseAddress(out.Web.SiteContactEmail)
		if e != nil || mailbox.Address != out.Web.SiteContactEmail {
			d.invalid("SITE_CONTACT_EMAIL")
		}
	}
	if len(out.Web.SiteAreaServed) > 160 || strings.ContainsAny(out.Web.SiteAreaServed, "\x00\r\n") {
		d.invalid("SITE_AREA_SERVED")
	}
	configurePolicies(&d, &out)
	validateFixedPolicies(&d)
	if d.err != nil {
		return runtimeConfig{}, d.err
	}
	return out, nil
}

func sentimentConfiguration(d *decoder) social.SentimentConfig {
	c := social.DefaultSentimentConfig()
	for _, v := range []struct {
		name  string
		value *int
		floor int
	}{{"SENTIMENT_K_ADULT", &c.AdultK, 5}, {"SENTIMENT_K_TEEN", &c.TeenK, 8}, {"DISSENT_K", &c.DissentK, 6}, {"DISSENT_AUDIENCE_FLOOR", &c.DissentAudience, 12}, {"DISSENT_WINDOWS_TO_LATCH", &c.DissentLatch, 2}, {"DISSENT_WINDOWS_TO_LAPSE", &c.DissentLapse, 2}, {"CONCERN_K1", &c.ConcernK1, 2}, {"CONCERN_K2", &c.ConcernK2, 4}, {"CONCERN_TEEN_K", &c.ConcernTeenK, 3}, {"CONCERN_AUDIENCE_FLOOR", &c.ConcernAudience, 8}, {"FORMATIVE_NOTE_COOLDOWN_DAYS", &c.CooldownDays, 14}, {"REACTION_ROW_RETENTION_DAYS", &c.RetentionDays, 1}} {
		*v.value = d.integer(v.name, *v.value, v.floor, 100000)
	}
	c.Mode = d.value("MODERATION_MODE", "automated+human")
	if c.Mode != "automated" && c.Mode != "automated+human" {
		d.invalid("MODERATION_MODE")
	}
	if c.DissentAudience < 2*c.DissentK {
		d.invalid("DISSENT_AUDIENCE_FLOOR")
	}
	if c.ConcernK2 < c.ConcernK1 {
		d.invalid("CONCERN_K2")
	}
	return c
}

func strongSecret(value string) bool {
	if len(value) < 32 || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	lower := strings.ToLower(value)
	for _, placeholder := range []string{"change-me", "changeme", "insecure", "example", "placeholder", "replace-me"} {
		if strings.Contains(lower, placeholder) {
			return false
		}
	}
	unique := map[byte]bool{}
	for i := 0; i < len(value); i++ {
		unique[value[i]] = true
	}
	return len(unique) >= 10
}
func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
}
func validHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if len(host) > 253 || strings.ContainsAny(host, "/:*\x00\r\n") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`).MatchString(label) {
			return false
		}
	}
	return true
}
func canonicalOrigin(raw, listen string) (string, error) {
	if raw == "" {
		host, port, err := net.SplitHostPort(listen)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || n < 1 || n > 65535 {
			return "", errors.New("--listen is invalid")
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		raw = "http://" + net.JoinHostPort(host, port)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !validHost(u.Hostname()) || strings.ContainsAny(raw, "\r\n\x00\\") {
		return "", errors.New("SITE_BASE_URL is invalid")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname())) {
		return "", errors.New("SITE_BASE_URL is invalid")
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 1 || p > 65535 {
			return "", errors.New("SITE_BASE_URL is invalid")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}
func safeHTTPS(raw string, allowLoopback bool) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Hostname() != "" && u.User == nil && u.Fragment == "" && strings.TrimSpace(raw) == raw && !strings.ContainsAny(raw, "\r\n\x00\\") && (u.Scheme == "https" || allowLoopback && u.Scheme == "http" && loopbackHost(u.Hostname()))
}
func databaseConfig(get environment, presence ...environmentPresence) (*pgxpool.Config, error) {
	raw := get("DATABASE_URL")
	if raw == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	uri, err := url.Parse(raw)
	if err != nil || (uri.Scheme != "postgres" && uri.Scheme != "postgresql" && uri.Scheme != "postgis") || uri.User == nil || uri.User.Username() == "" || uri.Hostname() == "" || uri.Path == "" || uri.Path == "/" {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	password, present := uri.User.Password()
	if !present || password == "" {
		return nil, errors.New("DATABASE_URL requires an explicit password")
	}
	for _, name := range []string{"service", "servicefile", "passfile", "password", "user", "host", "dbname", "database"} {
		if uri.Query().Has(name) {
			return nil, errors.New("DATABASE_URL cannot override its explicit credential source")
		}
	}
	if get("PGSERVICE") != "" {
		return nil, errors.New("PGSERVICE is unsupported; configure DATABASE_URL explicitly")
	}
	if uri.Scheme == "postgis" {
		uri.Scheme = "postgres"
		raw = uri.String()
	}
	c, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	// The explicit URL password bypasses pgpass lookup. Service-file settings
	// were rejected before pgx parsing, so credentials have one declared source.
	if c.ConnConfig.Host == "" || c.ConnConfig.Database == "" || c.ConnConfig.User == "" {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	d := newDecoder(get, presence...)
	d.fixedBool("DB_POOL_ENABLED", true)
	d.fixedBool("DB_POOLED", false)
	// pgx acquires using the caller's deadline; the retired Python pool's
	// separate queue timeout cannot be mapped faithfully to this pool.
	if d.isSet("DB_POOL_TIMEOUT") {
		d.invalid("DB_POOL_TIMEOUT")
	}
	c.MaxConns = int32(d.integer("DB_POOL_MAX_SIZE", 4, 2, 4))
	c.MinConns = int32(d.integer("DB_POOL_MIN_SIZE", 0, 0, int(c.MaxConns)))
	c.MaxConnLifetime = 30 * time.Minute
	c.MaxConnIdleTime = 5 * time.Minute
	c.ConnConfig.ConnectTimeout = 5 * time.Second
	c.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(d.integer("DB_STATEMENT_TIMEOUT_MS", 5000, 1, 30000))
	if d.err != nil {
		return nil, d.err
	}
	return c, nil
}
func heartbeatPing(endpoint string, client *http.Client) func(context.Context) bool {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	bounded := *client
	bounded.Timeout = 10 * time.Second
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return func(ctx context.Context) bool {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return false
		}
		response, err := bounded.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return err == nil && response.StatusCode >= 200 && response.StatusCode < 300
	}
}

// Decode strictly at both config and CLI boundaries: reject duplicate object
// keys, arrays/null, trailing documents and malformed JSON before unmarshalling.
func decodeJSONObject(raw []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("invalid JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		keyToken, err := dec.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("invalid JSON object")
		}
		seen[key] = true
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return errors.New("invalid JSON object")
		}
	}
	if token, err = dec.Token(); err != nil || token != json.Delim('}') {
		return errors.New("invalid JSON object")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid JSON object")
	}
	return json.Unmarshal(raw, target)
}
