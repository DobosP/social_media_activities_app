package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationConfig(t testing.TB) Config {
	t.Helper()
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := media.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return Config{PublicURL: "https://app.example", Secret: strings.Repeat("x", 48), Media: media.DefaultConfig(t.TempDir()), MediaStore: storage, Scanner: closedScanner{}, SiteRoot: root}
}

func TestNativeFreshDatabaseBootstrapAdoptionAndSessionSettings(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, *appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("native_app_bootstrap_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+` TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(*appDSN)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "7s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2s"
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	cfg.MaxConns = 1
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec(context.Background(), `DROP DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	config := integrationConfig(t)
	a, err := New(ctx, db, config, true)
	if err != nil {
		t.Fatal("fresh constructor", err)
	}
	owner := testdb.Actor(t, db, "generated-adopted-user", "adult")
	if _, err = New(ctx, db, config, true); err != nil {
		t.Fatal("idempotent adoption", err)
	}
	var id int64
	var statement, lock, path string
	if err = db.QueryRow(ctx, `SELECT id FROM accounts_user WHERE id=$1`, owner.ID).Scan(&id); err != nil || id != owner.ID {
		t.Fatal("existing account changed", err)
	}
	if err = db.QueryRow(ctx, `SELECT current_setting('statement_timeout'),current_setting('lock_timeout'),current_setting('search_path')`).Scan(&statement, &lock, &path); err != nil || statement != "7s" || lock != "2s" || path != "public" {
		t.Fatal("migration leaked pooled session settings", statement, lock, path, err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "https://app.example/api/places/", nil))
	if w.Code != 200 {
		t.Fatal("fresh public route", w.Code, w.Body.String())
	}
	a.Config.SecureSSLRedirect = true
	for _, probe := range []struct {
		path, peer, host string
		status           int
	}{
		{"/healthz", "127.0.0.1:1234", "app.example", 200},
		{"/healthz", "198.51.100.1:1234", "app.example", 301},
		{"/readyz", "127.0.0.1:1234", "app.example", 301},
		{"/healthz", "127.0.0.1:1234", "invalid.fixture.test", 400},
	} {
		r := httptest.NewRequest("GET", "http://app.example"+probe.path, nil)
		r.RemoteAddr = probe.peer
		r.Host = probe.host
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != probe.status {
			t.Fatal("production loopback liveness boundary", probe.path, probe.peer, w.Code)
		}
	}
	a.Ops.MarkDraining()
	for _, path := range []string{"/readyz", "/api/ready", "/api/v1/ready/"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "https://app.example"+path, nil))
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"draining":true`) {
			t.Fatal("draining readiness inconsistency", path, w.Code)
		}
	}
}

func TestNativeTokenRevocationReauthorizesOpenSocket(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	db := testdb.New(t, *appDSN, nil)
	config := integrationConfig(t)
	config.AllowedHosts = []string{"127.0.0.1"}
	a, err := New(context.Background(), db, config, true)
	if err != nil {
		t.Fatal(err)
	}
	owner := testdb.Actor(t, db, "generated-live-owner", "adult")
	place := testdb.Place(t, db, "generated-live-place", "osm")
	var typeID, thread int64
	if err = db.QueryRow(context.Background(), `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	activity, err := a.Social.CreateActivity(context.Background(), owner, social.ActivityInput{Place: place, ActivityType: typeID, Title: "Generated live activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(context.Background(), `SELECT id FROM social_thread WHERE activity_id=$1`, activity).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 40)
	if _, err = db.Exec(context.Background(), `INSERT INTO authtoken_token(key,user_id,created) VALUES($1,$2,now())`, token, owner.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.StartLive(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !a.Broker.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.Broker.Ready() {
		t.Fatal("broker unavailable")
	}
	server := httptest.NewServer(a)
	defer server.Close()
	header := http.Header{"Authorization": []string{"token " + token}}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+fmt.Sprintf("/ws/chat/%d/", thread), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal("native socket", err)
	}
	defer conn.CloseNow()
	if err = a.Accounts.RevokeAPIAccess(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", server.URL+"/api/accounts/me/", nil)
	request.Header = header.Clone()
	w := httptest.NewRecorder()
	a.ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal("revoked token HTTP", w.Code)
	}
	readctx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if err = conn.Write(readctx, websocket.MessageText, []byte(`{"body":"must never be committed"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = conn.Read(readctx); err == nil || readctx.Err() != nil {
		t.Fatal("open socket did not immediately revoke", err)
	}
	var posts int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM social_post WHERE thread_id=$1`, thread).Scan(&posts); err != nil || posts != 0 {
		t.Fatal("revoked socket wrote post", posts, err)
	}
}

func TestNativeBootstrapAdoptsPreinstalledSpatialExtensions(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, *appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("native_app_spatial_bootstrap_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+` TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(*appDSN)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 1
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec(context.Background(), `DROP DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	if _, err = db.Exec(ctx, `CREATE EXTENSION postgis;CREATE EXTENSION fuzzystrmatch;CREATE EXTENSION postgis_tiger_geocoder;CREATE EXTENSION postgis_topology;CREATE EXTENSION pg_trgm;CREATE EXTENSION vector`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal("preinstalled image extensions", err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO public.spatial_ref_sys(srid,auth_name,auth_srid,srtext,proj4text) VALUES(990999,'NATIVE-GENERATED-FIXTURE',990999,'synthetic extension-owned sentinel','synthetic projection sentinel')`); err != nil {
		t.Fatal(err)
	}
	config := integrationConfig(t)
	if _, err = New(ctx, db, config, true); err != nil {
		t.Fatal("native bootstrap over PostGIS image initialization", err)
	}
	var auth, srtext, projection string
	if err = db.QueryRow(ctx, `SELECT auth_name,srtext,proj4text FROM public.spatial_ref_sys WHERE srid=990999`).Scan(&auth, &srtext, &projection); err != nil || auth != "NATIVE-GENERATED-FIXTURE" || srtext != "synthetic extension-owned sentinel" || projection != "synthetic projection sentinel" {
		t.Fatal("bootstrap changed extension-owned reference data", err)
	}
	var extensions, schemas int
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_extension WHERE extname IN('postgis','postgis_tiger_geocoder','postgis_topology','fuzzystrmatch','pg_trgm','vector')),(SELECT count(*) FROM pg_namespace WHERE nspname IN('tiger','tiger_data','topology'))`).Scan(&extensions, &schemas); err != nil || extensions != 6 || schemas != 3 {
		t.Fatal("spatial extension adoption changed installation", extensions, schemas, err)
	}
}

func TestTrustedProxyThrottleIdentityAndBoundedSlidingWindow(t *testing.T) {
	a := &App{Config: Config{ProxyHops: 1, ThrottleAnonymous: 2, ThrottleUser: 3, ThrottleToken: 1}, proxyNetworks: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}}
	for _, c := range []struct{ peer, forwarded, want string }{
		{"192.0.2.4:3000", "203.0.113.2", "192.0.2.4:3000"},
		{"10.20.0.1:3000", "192.0.2.1, 203.0.113.2", "203.0.113.2:0"},
		{"10.20.0.1:3000", "invalid, 203.0.113.2", "10.20.0.1:3000"},
		{"[::ffff:10.20.0.1]:3000", "2001:db8::1", "[2001:db8::1]:0"},
	} {
		r := httptest.NewRequest("GET", "/api/places/", nil)
		r.RemoteAddr = c.peer
		r.Header.Set("X-Forwarded-For", c.forwarded)
		if got := a.forwardedPeer(r).RemoteAddr; got != c.want {
			t.Fatal("proxy trust", got, c.want)
		}
	}
}
func TestSharedAPIThrottleAcrossReplicas(t *testing.T) {
	db := testdb.New(t, *appDSN, func(ctx context.Context, db *pgxpool.Pool) error { return schema.Migrate(ctx, db) })
	actor := testdb.Actor(t, db, "generated-api-rate-actor", "adult")
	config := Config{ThrottleAnonymous: 2, ThrottleUser: 3, ThrottleToken: 1}
	replicas := []*App{{Config: config, rates: requestRates{store: budgets.New(db), secret: []byte("generated-rate-secret-at-least-32-bytes")}}, {Config: config, rates: requestRates{store: budgets.New(db), secret: []byte("generated-rate-secret-at-least-32-bytes")}}}
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest("GET", "/api/v1/places/", nil)
		if i%2 == 0 {
			r.URL.Path = "/api/places/"
		}
		r.RemoteAddr = "192.0.2.4:3000"
		if i == 1 {
			r.RemoteAddr = "[::ffff:192.0.2.4]:3000"
		}
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		w := httptest.NewRecorder()
		if allowed := replicas[i%2].admitAPI(w, replicas[i%2].forwardedPeer(r)); allowed != (i < 2) || i == 2 && (w.Code != 429 || w.Header().Get("Retry-After") == "") {
			t.Fatal("spoofed anonymous quota", i, w.Code)
		}
	}
	r := platform.WithActor(httptest.NewRequest("GET", "/api/places/", nil), actor)
	for i := 0; i < 4; i++ {
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1", i)
		if replicas[i%2].admitAPI(httptest.NewRecorder(), r) != (i < 3) {
			t.Fatal("user quota changed with IP", i)
		}
	}
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/api/auth/token/", nil)
		r.RemoteAddr = "192.0.2.4:3000"
		if replicas[i].admitAPI(httptest.NewRecorder(), r) != (i == 0) {
			t.Fatal("token scope not shared or separated")
		}
	}
}
func TestAPIAdmissionFailsClosedAndProbesRemainLive(t *testing.T) {
	a := &App{}
	w := httptest.NewRecorder()
	if a.admitAPI(w, httptest.NewRequest("GET", "/api/places/", nil)) || w.Code != 503 {
		t.Fatal("missing shared state admitted API", w.Code)
	}
	for _, path := range []string{"/api/health", "/api/v1/ready/", "/api/ops/csp-report/", "/home/"} {
		if !a.admitAPI(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil)) {
			t.Fatal("probe throttled", path)
		}
	}
}

type integrationCleanScanner struct{}

func (integrationCleanScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{Clean: true}, nil
}

func TestNativeApplicationDefaultInlineVideoUsesServerLifetime(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	db := testdb.New(t, *appDSN, nil)
	config := integrationConfig(t)
	config.Scanner = integrationCleanScanner{}
	boot, cancelBoot := context.WithCancel(context.Background())
	a, err := New(boot, db, config, true)
	cancelBoot()
	if err != nil {
		t.Fatal(err)
	}
	lifetime, cancelLife := context.WithCancel(context.Background())
	a.StartLive(lifetime)
	t.Cleanup(func() {
		cancelLife()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.StopBackground(ctx); err != nil {
			t.Error(err)
		}
	})
	owner := testdb.Actor(t, db, "generated-default-inline-owner", "adult")
	place := testdb.Place(t, db, "generated-default-inline-place", "osm")
	var typ int64
	if err = db.QueryRow(lifetime, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := a.Social.CreateActivity(lifetime, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Generated automatic video activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	session := strings.Repeat("i", 52)
	sum := sha256.Sum256([]byte(session))
	if err = a.Store.CreateSession(lifetime, authcore.Session{TokenHash: hex.EncodeToString(sum[:]), UserID: strconv.FormatInt(owner.ID, 10), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inline.mp4")
	if err = exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path).Run(); err != nil {
		t.Fatal(err)
	}
	video, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	csrf := strings.Repeat("c", 52)
	_ = writer.WriteField("csrfmiddlewaretoken", csrf)
	part, err := writer.CreateFormFile("attachment", "inline.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(video); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", fmt.Sprintf("https://app.example/activities/%d/post/", activity), bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Origin", "https://app.example")
	r.AddCookie(&http.Cookie{Name: "sessionid", Value: session})
	r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 302 && w.Code != 303 {
		t.Fatal("default inline upload admission", w.Code)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var ready bool
		if err = db.QueryRow(lifetime, `SELECT EXISTS(SELECT 1 FROM media_attachment m JOIN social_post p ON p.id=m.post_id WHERE p.author_id=$1 AND m.kind='video' AND m.status='ready' AND m.storage_key<>'' AND m.poster_storage_key<>'' AND m.source_storage_key='')`, owner.ID).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("default committed upload remained pending without an explicit job")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNativeClassicHTMLThreadVideoAboveEightMiB(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	db := testdb.New(t, *appDSN, nil)
	config := integrationConfig(t)
	config.Scanner = integrationCleanScanner{}
	config.AllowUserGroups = true
	a, err := New(context.Background(), db, config, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-html-video-owner", "adult")
	place := testdb.Place(t, db, "generated-html-video-place", "osm")
	var typ int64
	if err = db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := a.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Generated HTML video activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	group, err := a.Social.CreateGroup(ctx, owner, social.GroupInput{City: "Cluj-Napoca", Title: "Generated HTML video group", ActivityType: &typ})
	if err != nil {
		t.Fatal(err)
	}
	session := strings.Repeat("s", 52)
	digest := sha256.Sum256([]byte(session))
	if err = a.Store.CreateSession(ctx, authcore.Session{TokenHash: hex.EncodeToString(digest[:]), UserID: strconv.FormatInt(owner.ID, 10), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.mp4")
	if err = exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path).Run(); err != nil {
		t.Fatal(err)
	}
	video, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	video = append(video, bytes.Repeat([]byte{0}, (8<<20)+1-len(video))...)
	csrf := strings.Repeat("c", 52)
	for _, target := range []string{fmt.Sprintf("/activities/%d/post/", activity), fmt.Sprintf("/groups/%d/post/", group)} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("csrfmiddlewaretoken", csrf)
		part, err := writer.CreateFormFile("attachment", "fixture.mp4")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(video); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "https://app.example"+target, bytes.NewReader(body.Bytes()))
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Origin", "https://app.example")
		r.AddCookie(&http.Cookie{Name: "sessionid", Value: session})
		r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 302 && w.Code != 303 {
			t.Fatal("classic HTML video submission", target, w.Code)
		}
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM social_post p JOIN media_attachment m ON m.post_id=p.id WHERE p.author_id=$1 AND p.body='' AND m.kind='video' AND m.status='pending' AND m.storage_key='' AND m.source_storage_key<>'' AND m.byte_size>$2`, owner.ID, 8<<20).Scan(&count); err != nil || count != 2 {
		t.Fatal("HTML upload lost atomic quarantined media", count, err)
	}
	adminRequest := func(cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://app.example/admin/", nil)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "sessionid", Value: session})
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if adminRequest(false).Code != 404 || adminRequest(true).Code != 404 {
		t.Fatal("admin enumerated for anonymous/nonstaff")
	}
	if _, err = db.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(true); w.Code != 200 || !strings.Contains(w.Body.String(), "Model index") || !strings.Contains(w.Body.String(), "places.place") {
		t.Fatal("native admin service unreachable", w.Code)
	}
}
