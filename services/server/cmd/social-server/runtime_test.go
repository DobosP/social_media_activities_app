package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
)

// Every configuration test uses only this synthetic map. It never inspects the
// process environment, credential stores or the application's deployed env file.
func fixtureEnv(extra map[string]string) environment {
	values := map[string]string{"DJANGO_SECRET_KEY": "NativeFixture-Key-49Ak0s6ZY832T1vbClH5wPmQ", "SITE_BASE_URL": "http://127.0.0.1:8000"}
	for key, value := range extra {
		values[key] = value
	}
	return func(name string) string { return values[name] }
}
func fixtureOptions() cliOptions {
	return cliOptions{Listen: "127.0.0.1:8000", MediaDir: "var/media", Scratch: "var/media-work", StaticDir: "static", SiteRoot: "."}
}
func rawOptions(raw string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func TestRuntimeSourcePolicyMapping(t *testing.T) {
	env := fixtureEnv(map[string]string{
		"MEDIA_VIDEO_ENABLED": "off", "MEDIA_IMAGE_OUTPUT_FORMAT": "webp", "MEDIA_MAX_UPLOAD_BYTES": "1048576", "MEDIA_MAX_IMAGE_PIXELS": "2000000", "MEDIA_MAX_DIMENSION": "1200", "MEDIA_THUMB_DIMENSION": "400",
		"MEDIA_VIDEO_MAX_UPLOAD_BYTES": "2097152", "MEDIA_VIDEO_MAX_DURATION_SECONDS": "40", "MEDIA_VIDEO_MAX_SOURCE_SIDE": "1920", "MEDIA_VIDEO_TARGET_MAX_SIDE": "640", "MEDIA_VIDEO_THREADS": "1", "MEDIA_VIDEO_FFMPEG_TIMEOUT": "120", "MEDIA_VIDEO_PROBE_TIMEOUT": "20",
		"GUARDIAN_INVITE_TTL_DAYS": "3", "CONSENT_VALIDITY_DAYS": "180", "API_TOKEN_MAX_AGE_DAYS": "30", "ALLOW_MINOR_ONBOARDING": "false", "GROUPS_ALLOW_USER_CREATED": "true",
		"MESSAGING_RETENTION_DAYS": "90", "NOTIFICATION_RETENTION_DAYS": "0", "NOTIFICATION_RETENTION_BATCH": "400", "REVERIFY_SWEEP_BATCH": "700", "CONSENT_SWEEP_BATCH": "700", "REVERIFY_REMINDER_DAYS": "10", "CONSENT_RENEWAL_REMINDER_DAYS": "12",
		"SENTIMENT_K_ADULT": "7", "SENTIMENT_K_TEEN": "10", "DISSENT_K": "8", "DISSENT_AUDIENCE_FLOOR": "16", "CONCERN_K1": "3", "CONCERN_K2": "6", "MODERATION_MODE": "automated", "REACTION_ROW_RETENTION_DAYS": "60",
		"COMMUNITY_MIN_ACTIVITIES": "4", "COMMUNITY_K_ANON_FLOOR": "7", "COMMUNITY_MIN_DAYS": "3", "COMMUNITY_LOOKBACK_DAYS": "90", "CONNECTIONS_ALLOWED_COHORTS": "adult, teen",
		"TRUSTED_PROXY_CIDRS": "127.0.0.0/8,::1/128", "NUM_PROXIES": "2", "DRF_THROTTLE_ANON": "30/min", "DRF_THROTTLE_USER": "120/min", "DRF_THROTTLE_TOKEN_OBTAIN": "5/min",
		"DEFERRED_TASKS_BACKOFF_BASE": "15", "DEFERRED_TASKS_MAX_BACKOFF": "300",
	})
	config, err := configuration(env, fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}
	if config.App.Media.VideoEnabled || config.App.Media.ImageFormat != "WEBP" || config.App.Media.ImageMaxBytes != 1<<20 || config.App.Media.Threads != 1 || config.App.Media.CommandTimeout != 2*time.Minute {
		t.Fatal("codec policy not mapped")
	}
	if config.App.Accounts.GuardianInviteTTL != 72*time.Hour || config.App.Accounts.ConsentValidity != 180*24*time.Hour || config.App.Accounts.APITokenTTL != 30*24*time.Hour || !config.App.AllowUserGroups {
		t.Fatal("account policy not mapped")
	}
	if config.Jobs.MessagingRetentionDays != 90 || config.Jobs.NotificationRetentionDays != 0 || config.Jobs.SweepBatch != 700 || config.Jobs.ConsentReminderDays != 12 {
		t.Fatal("job policy not mapped")
	}
	if config.Sentiment.AdultK != 7 || config.Sentiment.TeenK != 10 || config.Sentiment.DissentAudience != 16 || config.Sentiment.Mode != "automated" || config.Community.AnonymousFloor != 7 || config.Connections["child"] || !config.Connections["teen"] {
		t.Fatal("publication policy not mapped")
	}
	if len(config.App.TrustedProxyCIDRs) != 2 || config.App.ProxyHops != 2 || config.App.ThrottleAnonymous != 30 || config.App.ThrottleUser != 120 || config.App.ThrottleToken != 5 {
		t.Fatal("proxy/throttle policy not mapped")
	}
	if config.BackoffBase != 15*time.Second || config.BackoffMax != 5*time.Minute || config.Jobs.Commons.TempDir != config.App.Media.ScratchDir {
		t.Fatal("private job runtime not mapped")
	}
}
func TestRuntimePublicationSourceIntegration(t *testing.T) {
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	env := fixtureEnv(map[string]string{"SITE_BASE_URL": "https://activities.fixture.test/", "ROEDU_SYNC_ENABLED": "true", "ROEDU_API_URL": "https://data.fixture.test", "ROEDU_API_KEY": "synthetic-read-only-key", "ROEDU_SYNC_CITY": "Cluj-Napoca", "INDEXNOW_ENABLED": "yes", "INDEXNOW_KEY": "fixture-indexnow-key", "AGENT_SNAPSHOT_DIR": snapshot, "OPS_HEARTBEAT_URL": "https://monitor.fixture.test/tick", "SITE_SAMEAS": "https://fixture.test/project,https://fixture.test/social", "SITE_AREA_SERVED": "Cluj-Napoca", "SITE_CONTACT_EMAIL": "team@fixture.test", "INGESTION_EXTRA_ADAPTERS": `{"roedu":"apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter"}`})
	config, err := configuration(env, fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}
	if config.App.PublicURL != "https://activities.fixture.test" || config.Jobs.SiteBaseURL != config.App.PublicURL || !config.Jobs.RoeduSyncEnabled || config.Jobs.Roedu == nil || config.Jobs.Roedu.APIKey != "synthetic-read-only-key" || !config.Jobs.IndexNowEnabled || config.Jobs.AgentSnapshotDir != snapshot || config.Jobs.Heartbeat == nil {
		t.Fatal("publication dependencies not mapped")
	}
	if config.Web.IndexNowKey != config.Jobs.IndexNowKey || config.Web.SnapshotDir != snapshot || len(config.Web.SiteSameAs) != 2 || config.Commands.Sources["roedu"] == nil {
		t.Fatal("public/source providers not mapped")
	}
}
func TestRuntimeRejectsMalformedAndUnsupportedOverridesByName(t *testing.T) {
	cases := []struct{ name, value string }{
		{"MEDIA_VIDEO_ENABLED", "invalid-bool-value"}, {"ALLOW_MINOR_ONBOARDING", "truthy-value"}, {"CHILD_PUBLIC_VENUES_ONLY", "false"}, {"MEDIA_REQUIRE_SCANNER", "false"},
		{"MEDIA_MAX_IMAGE_PIXELS", "30000001"}, {"MEDIA_VIDEO_THREADS", "3"}, {"MEDIA_VIDEO_MAX_DURATION_SECONDS", "91"}, {"CONSENT_VALIDITY_DAYS", "0"}, {"API_TOKEN_MAX_AGE_DAYS", "-1"},
		{"SENTIMENT_K_TEEN", "7"}, {"MODERATION_MODE", "unsupported-policy-value"}, {"CONNECTIONS_ALLOWED_COHORTS", "adult,,teen"}, {"GROUPS_USER_CREATION_COHORTS", "teen"},
		{"CLOSURE_REPORT_THRESHOLD", "1"}, {"FACT_QUORUM", "1"}, {"CORRECTION_QUORUM", "1"}, {"EDGE_QUORUM", "1"}, {"EVENT_REPORT_DECAY_SECONDS", "600"}, {"THREAD_POST_RATE_LIMIT", "999"},
		{"MEDIA_S3_SSE", "unsupported-encryption-value"}, {"MEDIA_S3_ADDRESSING_STYLE", "invalid-style-value"}, {"MEDIA_VIDEO_CRF", "18"}, {"MEDIA_VIDEO_PRESET", "fast"},
		{"IDENTITY_PROVIDER", "custom.python.IdentityProvider"}, {"DONATIONS_PROVIDER", "custom.python.PaymentProvider"}, {"BOOKING_PROVIDERS", `{"vendor":"custom.python.BookingProvider"}`}, {"INGESTION_EXTRA_ADAPTERS", `{"custom":"custom.python.Places"}`}, {"CHAT_MESSAGE_POLICY", "custom.python.MessagePolicy"},
		{"EUDI_TRUSTED_ISSUERS", `{"issuer":"one","issuer":"two"}`}, {"TRUSTED_PROXY_CIDRS", "untrusted-bad-cidr-value"}, {"NUM_PROXIES", "17"}, {"DJANGO_ALLOWED_HOSTS", "*"}, {"DRF_THROTTLE_ANON", "60/hour"},
		{"DJANGO_SECRET_KEY", "secret-placeholder-invalid-value"}, {"IDENTITY_BINDING_SECRET", "binding-placeholder-invalid-value"}, {"SITE_BASE_URL", "http://public.fixture.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := configuration(fixtureEnv(map[string]string{tc.name: tc.value}), fixtureOptions())
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("expected named failure, got %v", err)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatal("configuration value leaked")
			}
		})
	}
}
func TestRuntimeIdentityProviderAndLocalStorageGates(t *testing.T) {
	_, err := configuration(fixtureEnv(map[string]string{"IDENTITY_UNIQUENESS_ENFORCED": "true"}), fixtureOptions())
	if err == nil || !strings.Contains(err.Error(), "IDENTITY_BINDING_SECRET") {
		t.Fatal("dedicated identity secret not required")
	}
	_, err = configuration(fixtureEnv(map[string]string{"GOOGLE_OAUTH_CLIENT_ID": "synthetic-client"}), fixtureOptions())
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_OAUTH_CLIENT_ID") {
		t.Fatal("OAuth credential pair not required")
	}
	o := fixtureOptions()
	o.Dev = true
	_, err = configuration(fixtureEnv(map[string]string{"SITE_BASE_URL": "https://public.fixture.test"}), o)
	if err == nil {
		t.Fatal("public development origin accepted")
	}
	_, err = storageFromEnvironment(fixtureEnv(nil), filepath.Join(t.TempDir(), "media"), false)
	if err == nil {
		t.Fatal("production local storage accepted")
	}
	store, err := storageFromEnvironment(fixtureEnv(nil), filepath.Join(t.TempDir(), "media"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.(io.Closer).Close()
	s3 := fixtureEnv(map[string]string{"MEDIA_STORAGE_BACKEND": "s3", "MEDIA_S3_ENDPOINT_URL": "https://eu-store.fixture.test", "MEDIA_S3_BUCKET": "fixture-private-bucket", "MEDIA_S3_REGION": "eu-central-1", "AWS_ACCESS_KEY_ID": "synthetic-only", "AWS_SECRET_ACCESS_KEY": "synthetic-only", "MEDIA_EU_RESIDENCY_VERIFIED": "true", "MEDIA_PRIVATE_BUCKET_VERIFIED": "false"})
	if _, err = storageFromEnvironment(s3, "unused", false); err == nil || !strings.Contains(err.Error(), "MEDIA_PRIVATE_BUCKET_VERIFIED") {
		t.Fatal("unverified private bucket accepted")
	}
}
func TestScannersRemainFailClosedAndRigorouslyConfigured(t *testing.T) {
	scanner, documents, err := scannersFromEnvironment(fixtureEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scanner.Scan(context.Background(), media.ScanInput{SHA256: strings.Repeat("a", 64)}); !errors.Is(err, media.ErrScanner) {
		t.Fatal("empty scanner accepted")
	}
	if _, err = documents.ScanDocument(context.Background(), "synthetic-unread-path", 10); !errors.Is(err, media.ErrScanner) {
		t.Fatal("empty document scanner accepted")
	}
	for _, tc := range []struct{ name, value string }{{"MEDIA_IMAGE_SCANNER", "custom.python.Scanner"}, {"MEDIA_DOCUMENT_SCANNER", "custom.python.DocumentScanner"}, {"MEDIA_PERCEPTUAL_MAX_DISTANCE", "9"}, {"MEDIA_CSAM_HASH_BLOCKLIST", "broken-hash-value"}, {"MEDIA_REQUIRE_DOCUMENT_SCANNER", "true"}, {"MEDIA_CLAMD_PORT", "65536"}} {
		env := map[string]string{tc.name: tc.value}
		if tc.name == "MEDIA_CLAMD_PORT" {
			env["MEDIA_DOCUMENT_SCANNER"] = "clamd"
		}
		_, _, err = scannersFromEnvironment(fixtureEnv(env))
		if err == nil {
			t.Fatalf("accepted %s", tc.name)
		}
		if strings.Contains(err.Error(), tc.value) {
			t.Fatal("scanner configuration value leaked")
		}
	}
}
func TestJobOptionsBoundedAndStrict(t *testing.T) {
	valid, err := readJobOptions("-", strings.NewReader(`{"limit":2,"dry_run":true}`))
	if err != nil || len(valid) != 2 {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `{"limit":2} {}`, `{"limit":1,"limit":2}`, `{"limit":`, `{"":2}`, strings.Repeat(" ", (64<<10)+1)} {
		if _, err = readJobOptions("-", strings.NewReader(raw)); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	root := t.TempDir()
	file := filepath.Join(root, "options.json")
	if err = os.WriteFile(file, []byte(`{"limit":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readJobOptions(file, nil); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.json")
	if err = os.Symlink(file, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = readJobOptions(alias, nil); err == nil {
		t.Fatal("symlink options accepted")
	}
	if _, err = readJobOptions(root, nil); err == nil {
		t.Fatal("directory options accepted")
	}
}
func TestManualDueJobOptionValidation(t *testing.T) {
	for _, tc := range []struct{ name, opts string }{{"transcode_videos", `{"limit":2}`}, {"purge_expired_attachments", `{"limit":500}`}, {"process_deferred_tasks", `{"limit":200}`}, {"auto_complete_activities", `{"grace_hours":24}`}, {"expire_arrivals", `{"retention_hours":6}`}, {"send_activity_reminders", `{"within_hours":48}`}, {"sync_roedu", `{"city":"Cluj-Napoca"}`}, {"indexnow_batch_submit", `{"window_hours":12,"max_urls":5}`}} {
		if _, err := jobInvocation(tc.name, rawOptions(tc.opts)); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	for _, tc := range []struct{ name, opts string }{{"unregistered", `{}`}, {"transcode_videos", `{"limit":51}`}, {"purge_expired_attachments", `{"limit":1001}`}, {"process_deferred_tasks", `{"limit":-1}`}, {"auto_complete_activities", `{"grace_hours":"24"}`}, {"expire_arrivals", `{"retention_hours":3}`}, {"send_activity_reminders", `{"within_hours":0}`}, {"sync_roedu", `{"api_key":"never-log-this-value"}`}, {"indexnow_batch_submit", `{"max_urls":1001}`}, {"lift_suspensions", `{"force":true}`}} {
		if _, err := jobInvocation(tc.name, rawOptions(tc.opts)); err == nil {
			t.Fatalf("%s accepted invalid options", tc.name)
		}
	}
}
func TestCLIOneShotModesAreMutuallyExclusiveAndEarlySafe(t *testing.T) {
	for _, args := range [][]string{{"--job", "lift_suspensions", "--due"}, {"--migrate-only", "--due"}, {"--job-options", "-"}, {"unexpected"}, {"--due", "--job-options", "-"}} {
		if _, err := parseCLI(args, io.Discard); err == nil {
			t.Fatal("incompatible mode accepted")
		}
	}
	for _, args := range [][]string{{"--migrate-only"}, {"--migrate", "--job", "lift_suspensions"}, {"--job", "transcode_videos", "--job-options", "-"}, {"--due"}} {
		if _, err := parseCLI(args, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	get := environment(func(string) string { t.Fatal("early CLI path read environment"); return "" })
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--help"}, get, nil, &output, io.Discard); err != nil || !strings.Contains(output.String(), "migrate-only") {
		t.Fatal("help failed")
	}
	if err := run(context.Background(), []string{"--job", "unregistered"}, get, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("unknown job accepted")
	}
}
func TestCodecRuntimeVerificationCannotSilentlySkip(t *testing.T) {
	cfg := media.DefaultConfig(t.TempDir())
	cfg.FFmpeg = filepath.Join(cfg.ScratchDir, "missing-ffmpeg")
	processor, err := media.NewProcessor(cfg, denyScanner{}, denyDocuments{})
	if err != nil {
		t.Fatal(err)
	}
	if processor.CheckRuntime() == nil {
		t.Fatal("missing native media codec accepted")
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHeartbeatIsBoundedAndBestEffort(t *testing.T) {
	hits := 0
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		hits++
		if req.Method != http.MethodGet {
			t.Fatal("unexpected heartbeat method")
		}
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("unbounded heartbeat")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
	})}
	if !heartbeatPing("https://monitor.fixture.test/tick", client)(context.Background()) || hits != 1 {
		t.Fatal("heartbeat not delivered")
	}
	client.Transport = responseTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{"https://private.fixture.test"}}}, nil
	})
	if heartbeatPing("https://monitor.fixture.test/tick", client)(context.Background()) {
		t.Fatal("redirecting heartbeat succeeded")
	}
}
func TestExternalIngestionAddressGate(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.31.0.2", "100.64.1.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "::1", "::ffff:127.0.0.1", "2001:db8::1"} {
		if publicIngestionIP(net.ParseIP(raw)) {
			t.Fatal("private or reserved ingestion address accepted")
		}
	}
	if !publicIngestionIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}

func TestExplicitEmptySettingsDoNotRestoreIncompatibleDefaults(t *testing.T) {
	presence := environmentPresence(func(name string) bool { return name == "CONNECTIONS_ALLOWED_COHORTS" })
	config, err := configuration(fixtureEnv(map[string]string{"CONNECTIONS_ALLOWED_COHORTS": ""}), fixtureOptions(), presence)
	if err != nil || len(config.Connections) != 0 {
		t.Fatal("empty cohort policy did not disable connections")
	}
	for _, name := range []string{"MEDIA_FILE_COHORTS", "MEDIA_REQUIRE_SCANNER", "MEDIA_VIDEO_THREADS", "EUDI_TRUSTED_ISSUERS", "MEDIA_IMAGE_OUTPUT_FORMAT", "CHAT_MESSAGE_POLICY", "DRF_THROTTLE_USER"} {
		present := environmentPresence(func(key string) bool { return key == name })
		if _, err := configuration(fixtureEnv(map[string]string{name: ""}), fixtureOptions(), present); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("explicit empty %s restored incompatible defaults", name)
		}
	}
}
func TestRuntimeSourceSecurityLoggingAndMetadataSettings(t *testing.T) {
	prod, err := configuration(fixtureEnv(map[string]string{"SITE_BASE_URL": "https://site.fixture.test", "SITE_NAME": "Fixture Activities", "GOOGLE_SITE_VERIFICATION": "fixture-google-meta", "BING_SITE_VERIFICATION": "fixture-bing-meta", "MEDIA_VIDEO_INLINE_PROCESSING": "false", "DJANGO_HSTS_SECONDS": "63072000", "LOG_FORMAT": "json"}), fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !prod.App.SecureSSLRedirect || prod.App.HSTSSeconds != 63072000 || !prod.App.HSTSIncludeSubdomains || !prod.App.HSTSPreload || !prod.App.RequestLoggingEnabled || prod.App.LogFormat != "json" || prod.App.VideoInlineProcessing == nil || *prod.App.VideoInlineProcessing {
		t.Fatal("source production security/log/inline settings not mapped")
	}
	if prod.Web.SiteName != "Fixture Activities" || prod.Web.SiteGoogleVerification != "fixture-google-meta" || prod.Web.SiteBingVerification != "fixture-bing-meta" {
		t.Fatal("source public metadata not mapped")
	}
	devOptions := fixtureOptions()
	devOptions.Dev = true
	dev, err := configuration(fixtureEnv(nil), devOptions)
	if err != nil {
		t.Fatal(err)
	}
	if dev.App.SecureSSLRedirect || dev.App.HSTSSeconds != 0 || dev.App.RequestLoggingEnabled || dev.App.LogFormat != "plain" || !dev.App.Accounts.DemoEnabled {
		t.Fatal("development defaults not mapped")
	}
	for _, tc := range []struct{ name, value string }{{"REDIS_URL", "redis://unsupported-fixture"}, {"DJANGO_REQUIRE_SHARED_STATE", "true"}, {"SENTRY_DSN", "unsupported-fixture-dsn"}, {"LOG_FORMAT", "unsupported-format"}, {"LOG_LEVEL", "DEBUG"}, {"PERMISSIONS_POLICY", "camera=(self)"}, {"DJANGO_HSTS_SECONDS", "-1"}} {
		_, err := configuration(fixtureEnv(map[string]string{tc.name: tc.value}), fixtureOptions())
		if err == nil || !strings.Contains(err.Error(), tc.name) || strings.Contains(err.Error(), tc.value) {
			t.Fatalf("%s not rejected safely", tc.name)
		}
	}
}
func TestNativeS3SupportedPolicySettingsAndMalformedValues(t *testing.T) {
	settings := map[string]string{"MEDIA_STORAGE_BACKEND": "s3", "MEDIA_S3_ENDPOINT_URL": "https://store.fixture.test", "MEDIA_S3_BUCKET": "fixture-private-bucket", "MEDIA_S3_REGION": "eu-central-1", "AWS_ACCESS_KEY_ID": "synthetic-only", "AWS_SECRET_ACCESS_KEY": "synthetic-only", "MEDIA_EU_RESIDENCY_VERIFIED": "true", "MEDIA_PRIVATE_BUCKET_VERIFIED": "true", "MEDIA_S3_SSE": "AES256", "MEDIA_S3_ADDRESSING_STYLE": "virtual"}
	if _, err := storageFromEnvironment(fixtureEnv(settings), "unused", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"MEDIA_S3_SSE", "MEDIA_S3_ADDRESSING_STYLE"} {
		copy := map[string]string{}
		for key, value := range settings {
			copy[key] = value
		}
		copy[name] = "invalid-fixture-value"
		if _, err := storageFromEnvironment(fixtureEnv(copy), "unused", false); err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "invalid-fixture-value") {
			t.Fatal("malformed S3 setting accepted or exposed")
		}
	}
}
func TestCLISourceDueReminderOptionIsBounded(t *testing.T) {
	options, err := parseCLI([]string{"--due", "--reminder-within-hours", "48"}, io.Discard)
	if err != nil || options.ReminderWithinHours != 48 {
		t.Fatal("source due reminder override rejected")
	}
	for _, args := range [][]string{{"--reminder-within-hours", "48"}, {"--due", "--reminder-within-hours", "0"}, {"--due", "--reminder-within-hours", "8761"}} {
		if _, err := parseCLI(args, io.Discard); err == nil {
			t.Fatal("invalid due reminder option accepted")
		}
	}
}

func TestDevelopmentRequiresLoopbackListenerAndCanonicalOrigin(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:8000", ":8000", "[::]:8000", "public.fixture.test:8000"} {
		o := fixtureOptions()
		o.Dev = true
		o.Listen = listen
		if _, err := configuration(fixtureEnv(nil), o); err == nil || !strings.Contains(err.Error(), "--listen") {
			t.Fatal("development fixture exposed on nonloopback listener")
		}
	}
	for _, listen := range []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"} {
		o := fixtureOptions()
		o.Dev = true
		o.Listen = listen
		if _, err := configuration(fixtureEnv(nil), o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdministratorBootstrapAcceptsOnlySecretStdinBeforeEnvironment(t *testing.T) {
	get := environment(func(string) string { t.Fatal("bootstrap file refusal read environment"); return "" })
	for _, name := range []string{"createsuperuser", "create_superuser"} {
		for _, arguments := range [][]string{{"--job", name}, {"--job", name, "--job-options", "private-options.json"}} {
			if err := run(context.Background(), arguments, get, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "stdin") {
				t.Fatal("bootstrap credentials accepted outside secret stdin")
			}
		}
	}
}

func TestExplicitContainerDevelopmentListenerException(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:8000", ":8000", "[::]:8000"} {
		o := fixtureOptions()
		o.Dev = true
		o.DevContainer = true
		o.Listen = listen
		if _, err := configuration(fixtureEnv(nil), o); err != nil {
			t.Fatal("explicit container development listener refused", err)
		}
	}
	o := fixtureOptions()
	o.DevContainer = true
	o.Listen = "0.0.0.0:8000"
	if _, err := configuration(fixtureEnv(map[string]string{"DJANGO_DEBUG": "true"}), o); err == nil || !strings.Contains(err.Error(), "--dev") {
		t.Fatal("container exception accepted without explicit --dev")
	}
	if _, err := parseCLI([]string{"--dev-container"}, io.Discard); err == nil {
		t.Fatal("container exception accepted without --dev flag")
	}
	o.Dev = true
	if _, err := configuration(fixtureEnv(map[string]string{"SITE_BASE_URL": "https://public.fixture.test"}), o); err == nil || !strings.Contains(err.Error(), "SITE_BASE_URL") {
		t.Fatal("public development canonical origin accepted")
	}
	if _, err := parseCLI([]string{"--dev", "--dev-container", "--listen", "0.0.0.0:8000"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}
