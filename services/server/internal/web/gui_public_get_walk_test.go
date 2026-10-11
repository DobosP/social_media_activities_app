package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This disjoint M0 producer uses exact historical template/catalog inputs and
// the original native HTTP implementation. It never rewrites the original14,
// calls today's nonce-modified templates original, or follows a redirect.
var guiPublicGetWalkOutput = flag.String("gui-public-get-walk-output", "", "fresh private directory for the explicit synthetic historical G1 registered GET walk; requires web-domain-test-dsn")

const guiPublicGetHistory = "75dc717377f92b21ed99aabbe1f290d92535523e"
const guiPublicGetSourceSHA = "e92e1d44feee05d052bc733f68db102d45b76d9fbcd927453bdace7392757ac3"
const guiPublicGetCasesSHA = "35b30b7a2520750525fd2ee380d2f5597aa6fe6b374c92d5cd9a6db030abbce3"

//go:embed testdata/gui-public-get-walk/source.json
var guiPublicGetSourceBytes []byte

//go:embed testdata/gui-public-get-walk/cases.json
var guiPublicGetCaseBytes []byte

type guiPublicGetSource struct {
	Schema       int    `json:"schema"`
	SourceCommit string `json:"source_commit"`
	Producers    []struct {
		Path, Relation   string
		HistoricalSHA256 string `json:"historical_sha256"`
		CurrentSHA256    string `json:"current_sha256"`
	} `json:"producers"`
	Files []struct {
		Path, SHA256, Base64 string
		Bytes                int
	} `json:"files"`
	PreservedSourceInputs []struct {
		Path, SHA256 string
		Bytes        int
		FullMode     uint32 `json:"full_mode"`
	} `json:"preserved_source_inputs"`
}

type guiPublicGetCase struct {
	ID, Route, Path, Template, State, Language, Profile string
	Status                                              int
	Contains                                            []string
	Filtered                                            bool
	JSONLD                                              bool `json:"json_ld"`
}

func guiPublicGetSources(raw []byte) (guiPublicGetSource, map[string][]byte, error) {
	var source guiPublicGetSource
	if json.Unmarshal(raw, &source) != nil || source.Schema != 1 || source.SourceCommit != guiPublicGetHistory || len(source.Files) != 20 || len(source.Producers) != 14 || len(source.PreservedSourceInputs) != 86 {
		return source, nil, fmt.Errorf("historical-source-frame")
	}
	files := map[string][]byte{}
	for _, row := range source.Files {
		if filepath.ToSlash(filepath.Clean(row.Path)) != row.Path || filepath.IsAbs(row.Path) || strings.Contains(row.Path, "\\") || strings.HasPrefix(row.Path, "../") || !(strings.HasPrefix(row.Path, "templates/") || strings.HasPrefix(row.Path, "apps/web/templates/web/") || row.Path == "locale/ro/LC_MESSAGES/django.po") {
			return source, nil, fmt.Errorf("historical-source-path")
		}
		if _, exists := files[row.Path]; exists {
			return source, nil, fmt.Errorf("historical-source-duplicate")
		}
		data, err := base64.StdEncoding.DecodeString(row.Base64)
		if err != nil || len(data) != row.Bytes || row.Bytes < 1 || row.Bytes > 512<<10 || guiPublicHash(data) != row.SHA256 {
			return source, nil, fmt.Errorf("historical-source-bytes")
		}
		files[row.Path] = data
	}
	if len(files["templates/base.html"]) == 0 || len(files["locale/ro/LC_MESSAGES/django.po"]) == 0 {
		return source, nil, fmt.Errorf("historical-source-required")
	}
	return source, files, nil
}

// These two exact inverses are the previously reviewed additive FS seams. The
// OS constructor/Render path and the original catalog parser remain in use.
func guiPublicGetProducerOriginal(name string, raw []byte) ([]byte, error) {
	type edit struct{ after, before string }
	var edits []edit
	switch name {
	case "services/server/internal/web/renderer.go":
		edits = []edit{{"loader     pongo2.TemplateLoader", "loader     *templateLoader"}}
	case "services/server/internal/web/i18n.go":
		edits = []edit{
			{"\t\"io\"\n", ""},
			{"\tresult, _ := parseCatalog(file) // Preserve the original disk path's partial-catalog behavior.\n\treturn result\n}\n\nfunc parseCatalog(reader io.Reader) (translationCatalog, error) {\n", ""},
			{"scanner := bufio.NewScanner(reader)", "scanner := bufio.NewScanner(file)"},
			{"return result, scanner.Err()", "return result"},
		}
	}
	for _, e := range edits {
		if bytes.Count(raw, []byte(e.after)) != 1 {
			return nil, fmt.Errorf("native-producer-inverse")
		}
		raw = bytes.Replace(raw, []byte(e.after), []byte(e.before), 1)
	}
	return raw, nil
}

// The repaired default profile proves historical preimage custody only. It
// cannot authorize the old HTTP capture or supply substituted current tuples
// to any source reader/receiver. Both reviewed production afterimages are
// required together; the original physical-old capture binder stays below.
type guiPublicGetRepairInput struct {
	raw     []byte
	binding guiG1Binding
}

type guiPublicGetRepairRow struct {
	path, beforeSHA, afterSHA string
	beforeBytes, afterBytes   int
	before, after             string
}

func guiPublicGetRepairRows() []guiPublicGetRepairRow {
	return []guiPublicGetRepairRow{
		{
			path:        "services/server/internal/web/renderer.go",
			beforeSHA:   "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68",
			afterSHA:    "7c1ec3a7601072ce88e2c4d39184faa9fdb734066681980ebad1ab0a04a98280",
			beforeBytes: 5510,
			afterBytes:  5539,
			before:      `"is_admin": actor.Admin(), "username": actor.Username`,
			after:       `"is_admin": actor.Admin(), "public_id": actor.PublicID, "username": actor.Username`,
		},
		{
			path:        "services/server/internal/web/server.go",
			beforeSHA:   "0c4208333d354d2256f275459a420ac5f2ff936abfab6c13cc4b8b6ca4fdcc09",
			afterSHA:    "5a55f84558c62d240fb48d9f147fea623ec863435696b4b6eb50127664f4c340",
			beforeBytes: 10960,
			afterBytes:  11113,
			before:      "\t\tdata[\"avatar_uri\"] = avatar\n\t}\n\tif err = s.Renderer.Render",
			after:       "\t\tdata[\"avatar_uri\"] = avatar\n\t\tif user, ok := data[\"user\"].(map[string]any); ok && user[\"id\"] == actor.ID && user[\"public_id\"] == actor.PublicID {\n\t\t\tuser[\"avatar_uri\"] = avatar\n\t\t}\n\t}\n\tif err = s.Renderer.Render",
		},
	}
}

func guiPublicGetRepairInputs(root string, source guiPublicGetSource) (map[string]guiPublicGetRepairInput, error) {
	names := map[string]bool{}
	for _, row := range source.PreservedSourceInputs {
		names[row.Path] = true
	}
	for _, row := range source.Producers {
		names[row.Path] = true
	}
	inputs := map[string]guiPublicGetRepairInput{}
	for name := range names {
		raw, binding, err := guiCurrentTransportReadBinding(root, name, 2<<20)
		if err != nil {
			return nil, fmt.Errorf("repair-current-input")
		}
		inputs[name] = guiPublicGetRepairInput{raw, binding}
	}
	return inputs, nil
}

func guiPublicGetRepairHistoricalProof(source guiPublicGetSource, inputs map[string]guiPublicGetRepairInput) error {
	if source.Schema != 1 || source.SourceCommit != guiPublicGetHistory || len(source.PreservedSourceInputs) != 86 || len(source.Producers) != 14 || len(inputs) != 93 {
		return fmt.Errorf("repair-source-frame")
	}
	preimages := map[string][]byte{}
	for name, input := range inputs {
		if len(input.raw) != input.binding.Bytes || guiPublicHash(input.raw) != input.binding.SHA256 || input.binding.FullMode&0o170000 != 0o100000 || input.binding.FullMode&0o7000 != 0 {
			return fmt.Errorf("repair-current-input")
		}
		preimages[name] = input.raw
	}
	for _, row := range guiPublicGetRepairRows() {
		input, exists := inputs[row.path]
		if !exists || input.binding.FullMode != 0o100664 || len(input.raw) != row.afterBytes || guiPublicHash(input.raw) != row.afterSHA || bytes.Count(input.raw, []byte(row.after)) != 1 {
			return fmt.Errorf("repair-complete-current-profile")
		}
		original := bytes.Replace(input.raw, []byte(row.after), []byte(row.before), 1)
		if len(original) != row.beforeBytes || guiPublicHash(original) != row.beforeSHA {
			return fmt.Errorf("repair-exact-preimage")
		}
		preimages[row.path] = original // In-memory custody proof, never a live source binding.
	}
	seen := map[string]bool{}
	preservedRepairs := 0
	for _, row := range source.PreservedSourceInputs {
		if seen[row.Path] || filepath.ToSlash(filepath.Clean(row.Path)) != row.Path || filepath.IsAbs(row.Path) || strings.Contains(row.Path, "\\") || strings.HasPrefix(row.Path, "../") {
			return fmt.Errorf("repair-preserved-path")
		}
		seen[row.Path] = true
		input, exists := inputs[row.Path]
		raw := preimages[row.Path]
		if !exists || len(raw) != row.Bytes || guiPublicHash(raw) != row.SHA256 || input.binding.FullMode != row.FullMode {
			return fmt.Errorf("repair-preserved-binding")
		}
		if row.Path == "services/server/internal/web/renderer.go" || row.Path == "services/server/internal/web/server.go" {
			preservedRepairs++
		}
	}
	seen = map[string]bool{}
	producerRepairs := 0
	for _, row := range source.Producers {
		if seen[row.Path] || !strings.HasPrefix(row.Path, "services/server/internal/web/") || filepath.ToSlash(filepath.Clean(row.Path)) != row.Path {
			return fmt.Errorf("repair-producer-path")
		}
		seen[row.Path] = true
		raw, exists := preimages[row.Path]
		if !exists || len(raw) > 512<<10 || guiPublicHash(raw) != row.CurrentSHA256 {
			return fmt.Errorf("repair-producer-preimage")
		}
		original, err := guiPublicGetProducerOriginal(row.Path, raw)
		if err != nil || guiPublicHash(original) != row.HistoricalSHA256 {
			return fmt.Errorf("repair-producer-history")
		}
		if row.Path == "services/server/internal/web/renderer.go" || row.Path == "services/server/internal/web/server.go" {
			producerRepairs++
		}
	}
	if preservedRepairs != 2 || producerRepairs != 2 {
		return fmt.Errorf("repair-two-producer-relations")
	}
	return nil
}

func guiPublicGetBindRequestedProducers(root string, source guiPublicGetSource, capture bool) error {
	if capture {
		return guiPublicGetBindProducers(root, source) // Strict physical-old identity before any PG/output.
	}
	inputs, err := guiPublicGetRepairInputs(root, source)
	if err != nil {
		return err
	}
	return guiPublicGetRepairHistoricalProof(source, inputs)
}

func guiPublicGetBindProducers(root string, source guiPublicGetSource) error {
	for _, row := range source.PreservedSourceInputs {
		if filepath.ToSlash(filepath.Clean(row.Path)) != row.Path || filepath.IsAbs(row.Path) || strings.HasPrefix(row.Path, "../") {
			return fmt.Errorf("preserved-source-path")
		}
		name := filepath.Join(root, filepath.FromSlash(row.Path))
		raw, err := guiPublicBoundFile(name, 2<<20)
		info, statErr := os.Lstat(name)
		if err != nil || statErr != nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || len(raw) != row.Bytes || guiPublicHash(raw) != row.SHA256 || (0o100000|uint32(info.Mode().Perm())) != row.FullMode {
			return fmt.Errorf("preserved-source-binding")
		}
	}
	seen := map[string]bool{}
	for _, row := range source.Producers {
		if seen[row.Path] || !strings.HasPrefix(row.Path, "services/server/internal/web/") || filepath.ToSlash(filepath.Clean(row.Path)) != row.Path {
			return fmt.Errorf("native-producer-path")
		}
		seen[row.Path] = true
		raw, err := guiPublicBoundFile(filepath.Join(root, filepath.FromSlash(row.Path)), 512<<10)
		if err != nil || guiPublicHash(raw) != row.CurrentSHA256 {
			return fmt.Errorf("native-producer-current")
		}
		original, err := guiPublicGetProducerOriginal(row.Path, raw)
		if err != nil || guiPublicHash(original) != row.HistoricalSHA256 {
			return fmt.Errorf("native-producer-history")
		}
	}
	return nil
}

func guiPublicGetCases() ([]guiPublicGetCase, error) {
	var registry struct {
		Schema int
		Cases  []guiPublicGetCase
	}
	if guiPublicHash(guiPublicGetCaseBytes) != guiPublicGetCasesSHA || json.Unmarshal(guiPublicGetCaseBytes, &registry) != nil || registry.Schema != 1 || len(registry.Cases) != 37 {
		return nil, fmt.Errorf("GET-case-registry")
	}
	seen := map[string]bool{}
	for _, c := range registry.Cases {
		_, routeExists := routePaths[c.Route]
		if seen[c.ID] || !routeExists || !strings.HasPrefix(c.Path, "/") || strings.HasPrefix(c.Path, "//") || (c.State != "empty" && c.State != "populated") || (c.Language != "en" && c.Language != "ro") || (c.Profile != "default" && c.Profile != "contrast") || (c.Status != 200 && c.Status != 302 && c.Status != 404) || (c.Status == 200) != (c.Template != "") {
			return nil, fmt.Errorf("GET-case-identity")
		}
		seen[c.ID] = true
	}
	return registry.Cases, nil
}

func guiPublicGetRequireDSN(value string) error {
	if value == "" {
		return fmt.Errorf("explicit-disposable-web-DSN-required-no-skip")
	}
	return nil
}

func guiPublicGetMaterialize(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, raw := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("historical snapshot write failed")
		}
	}
	t.Cleanup(func() {
		for name, want := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			raw, err := guiPublicBoundFile(path, 512<<10)
			info, statErr := os.Lstat(path)
			if err != nil || statErr != nil || info.Mode().Perm() != 0o600 || !bytes.Equal(raw, want) {
				t.Error("historical snapshot changed")
			}
		}
	})
	return root
}

type guiPublicGetFixture struct {
	server                 *Server
	place, event, campaign int64
}

func guiPublicGetFixtureFor(t *testing.T, root, state string) guiPublicGetFixture {
	t.Helper()
	db := testdb.New(t, *webDomainDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	auth, err := authcore.New(authcore.Config{PublicURL: "https://fixture.local"}, accounts.NewStore(db))
	if err != nil {
		t.Fatal("fixture auth construction failed")
	}
	s := NewServer(db, auth, nil, social.New(db, platform.RecordAudit), nil, http.NewServeMux(), Config{Root: root, PublicURL: "https://fixture.local", SiteName: "Synthetic original G1 fixture"})
	s.Catalog, s.Donations = catalog.New(db), donations.New(db, donations.Config{Provider: "dev"})
	f := guiPublicGetFixture{server: s}
	if state == "empty" {
		return f
	}
	ctx := context.Background()
	f.place = testdb.Place(t, db, "Original synthetic hall", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal("fixture taxonomy missing")
	}
	if _, err := db.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,updated_at,created_at) VALUES($1,$2,'inferred',0.9,'osm','fixture',false,now(),now());`, f.place, typ); err != nil {
		t.Fatal("fixture edge insert failed")
	}
	if _, err := db.Exec(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES('Cluj-Napoca','cluj-napoca','Cluj-Napoca','city',0,true,now())`); err != nil {
		t.Fatal("fixture area insert failed")
	}
	f.event = publicEventFixture(t, s, "Original synthetic event", f.place, typ)
	if _, err := db.Exec(ctx, `UPDATE events_event SET starts_at='2035-01-02T12:00:00Z' WHERE id=$1`, f.event); err != nil {
		t.Fatal("synthetic event time binding failed")
	}
	actor := testdb.Actor(t, db, "original-g1-synthetic-adult", "adult")
	min := 1
	activity, err := s.Social.CreateActivity(ctx, actor, social.ActivityInput{Place: f.place, ActivityType: typ, Title: "Original synthetic meetup", Description: "Synthetic public fixture.", StartsAt: time.Date(2035, 1, 2, 12, 0, 0, 0, time.UTC), MinToGo: &min})
	if err != nil || s.Social.SetActivityListing(ctx, actor, activity, true) != nil {
		t.Fatal("synthetic adult public meetup seed failed")
	}
	// Fixture rows only: ordinary user-group creation stays disabled. This does
	// not call/qualify admission or change AllowUserGroups/any policy default.
	if _, err := db.Exec(ctx, `INSERT INTO social_group(owner_id,area_id,category_id,activity_type_id,tier,cohort,title,description,status,is_hidden,is_publicly_listed,is_staff_curated,created_at,updated_at) SELECT $1,ar.id,t.category_id,t.id,'type','adult','Original synthetic group','Synthetic public fixture.','active',false,true,false,now(),now() FROM communities_area ar CROSS JOIN taxonomy_activitytype t WHERE ar.slug='cluj-napoca' AND t.id=$2`, actor.ID, typ); err != nil {
		t.Fatal("synthetic public group row insert failed")
	}
	var partner int64
	if err := db.QueryRow(ctx, `INSERT INTO places_partner(name,kind,blurb,place_id,website,is_verified,is_active,created_at,updated_at) VALUES('Original synthetic partner','library','Synthetic acknowledgement',$1,'https://fixture.invalid',true,true,now(),now()) RETURNING id`, f.place).Scan(&partner); err != nil {
		t.Fatal("fixture partner insert failed")
	}
	if err := db.QueryRow(ctx, `INSERT INTO donations_campaign(title,slug,description,goal_cents,currency,is_active,outcome,closed_at,partner_id,created) VALUES('Original synthetic campaign','synthetic-campaign','Synthetic public mission',1000,'EUR',true,'',NULL,$1,now()) RETURNING id`, partner).Scan(&f.campaign); err != nil {
		t.Fatal("fixture campaign insert failed")
	}
	if _, err := db.Exec(ctx, `INSERT INTO donations_campaign(title,slug,description,goal_cents,currency,is_active,outcome,closed_at,partner_id,created) VALUES('Original synthetic closed campaign','synthetic-closed','',1000,'EUR',false,'Books delivered.','2026-01-02T12:00:00Z',$1,now())`, partner); err != nil {
		t.Fatal("fixture completed campaign insert failed")
	}
	return f
}

func guiPublicGetCheck(c guiPublicGetCase, w *httptest.ResponseRecorder, loads []guiPublicTemplateLoad, canonical string) error {
	if w.Code != c.Status {
		return fmt.Errorf("status")
	}
	if c.Status == 302 {
		if w.Header().Get("Location") != "/login/?next="+url.QueryEscape(c.Path) || len(loads) != 0 || w.Body.Len() == 0 {
			return fmt.Errorf("anonymous-series-redirect")
		}
		return nil
	}
	if c.Status == 404 {
		if len(loads) != 0 || w.Body.Len() == 0 {
			return fmt.Errorf("missing-resource")
		}
		return nil
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		return fmt.Errorf("content-type")
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") {
		return fmt.Errorf("cache")
	}
	if len(loads) < 2 || loads[0].Name != c.Template {
		return fmt.Errorf("template")
	}
	base := false
	for _, load := range loads {
		if load.Name == "base.html" {
			base = true
		}
		if load.Bytes == 0 || len(load.SHA256) != 64 {
			return fmt.Errorf("template-stream")
		}
	}
	if !base {
		return fmt.Errorf("template")
	}
	// These assertions use the exact source-bound serialization above. This is
	// neither an HTML normalizer nor a general DOM parser/CSP acceptance check.
	body := w.Body.String()
	htmlTags := regexp.MustCompile(`<html [^>]*>`).FindAllString(body, -1)
	if len(htmlTags) != 1 || !strings.Contains(htmlTags[0], `lang="`+c.Language+`"`) {
		return fmt.Errorf("language")
	}
	if c.Profile == "contrast" {
		for _, attribute := range []string{`data-theme="contrast"`, `data-text="larger"`, `data-motion="reduce"`} {
			if !strings.Contains(htmlTags[0], attribute) {
				return fmt.Errorf("display-profile")
			}
		}
	}
	if len(regexp.MustCompile(`<h1(?: [^>]*)?>`).FindAllString(body, -1)) != 1 || len(regexp.MustCompile(`<main [^>]*id="main"[^>]*>`).FindAllString(body, -1)) != 1 {
		return fmt.Errorf("heading")
	}
	canonicals := regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`).FindAllStringSubmatch(body, -1)
	if len(canonicals) != 1 || html.UnescapeString(canonicals[0][1]) != canonical {
		return fmt.Errorf("canonical")
	}
	wantRobots := "index, follow"
	if c.Filtered {
		wantRobots = "noindex, follow"
	}
	robots := regexp.MustCompile(`<meta name="robots" content="([^"]*)">`).FindAllStringSubmatch(body, -1)
	if len(robots) != 1 || robots[0][1] != wantRobots {
		return fmt.Errorf("robots")
	}
	for _, link := range []string{"/privacy/", "/terms/", "/login/", "/register/"} {
		if !regexp.MustCompile(`<a [^>]*href="` + regexp.QuoteMeta(link) + `"`).MatchString(body) {
			return fmt.Errorf("footer")
		}
	}
	ld := regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`).FindAllStringSubmatch(body, -1)
	if c.JSONLD && len(ld) == 0 {
		return fmt.Errorf("structured-data")
	}
	for _, script := range ld {
		var value any
		if json.Unmarshal([]byte(script[1]), &value) != nil {
			return fmt.Errorf("structured-data")
		}
	}
	for _, want := range c.Contains {
		if !strings.Contains(body, html.EscapeString(want)) {
			return fmt.Errorf("fixture-content")
		}
	}
	if c.Route == "places_map" {
		categories := strings.Count(body, `data-map-filter="category"`)
		if c.State == "empty" && categories != 0 || c.State == "populated" && categories == 0 {
			return fmt.Errorf("map-category-state")
		}
	}
	return nil
}

func guiPublicGetBoundInputs(root string) (map[string]map[string]any, error) {
	names, err := guiPublicSources(root)
	if err != nil {
		return nil, err
	}
	var source guiPublicGetSource
	if json.Unmarshal(guiPublicGetSourceBytes, &source) != nil {
		return nil, fmt.Errorf("source-input-frame")
	}
	for _, row := range source.PreservedSourceInputs {
		names[row.Path] = ""
	}
	for _, row := range source.Producers {
		names[row.Path] = ""
	}
	for _, name := range []string{"gui_public_get_walk_test.go", "public_details.go", "public_landings.go", "payments.go", "payments_test.go", "public_pages_test.go", "venue_claim_access_case_port_test.go"} {
		names["services/server/internal/web/"+name] = ""
	}
	names["services/server/internal/web/testdata/gui-public-get-walk/source.json"] = ""
	names["services/server/internal/web/testdata/gui-public-get-walk/cases.json"] = ""
	rows := map[string]map[string]any{}
	for name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		raw, err := guiPublicBoundFile(path, 2<<20)
		info, statErr := os.Lstat(path)
		if err != nil || statErr != nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return nil, fmt.Errorf("source-input")
		}
		rows[name] = map[string]any{"bytes": len(raw), "sha256": guiPublicHash(raw), "full_mode": 0o100000 | uint32(info.Mode().Perm())}
	}
	return rows, nil
}

func TestGUIPublicGetWalk(t *testing.T) {
	if *guiPublicCurrentGetTransport {
		guiPublicCurrentGetTransportWalk(t)
		return
	}
	root, err := filepath.Abs("../../../..")
	if err != nil || guiPublicHash(guiPublicGetSourceBytes) != guiPublicGetSourceSHA {
		t.Fatal("historical source identity differs")
	}
	source, files, err := guiPublicGetSources(guiPublicGetSourceBytes)
	if err != nil || guiPublicGetBindRequestedProducers(root, source, *guiPublicGetWalkOutput != "") != nil {
		t.Fatal("historical native producer binding differs")
	}
	cases, err := guiPublicGetCases()
	if err != nil {
		t.Fatal(err)
	}
	if *guiPublicGetWalkOutput == "" {
		t.Log("HTTP NOT REQUESTED: historical preimage/custody and exact current two-repair bytes verified; no old/current producer equivalence, PG, capture, normalization or registered-GET qualification")
		return
	}
	if err := guiPublicGetRequireDSN(*webDomainDSN); err != nil {
		t.Fatal(err) // This precedes testdb.New; missing prerequisites cannot skip.
	}
	if *guiPublicGoldenReference == "" {
		t.Fatal("existing original15 reference is required for the opt-in walk")
	}
	before, err := guiPublicGetBoundInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	output, err := guiPublicOutput(*guiPublicGetWalkOutput)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	var reference map[string]guiPublicReferenceRecord
	if *guiPublicGoldenReference != "" {
		checkpoint, err := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
		if err != nil {
			t.Fatal(err)
		}
		reference, err = guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
		if err != nil {
			t.Fatal(err)
		}
	}
	records := []map[string]any{}
	t.Cleanup(func() {
		after, err := guiPublicGetBoundInputs(root)
		unchanged := err == nil && reflect.DeepEqual(before, after)
		if !unchanged || guiPublicGetBindProducers(root, source) != nil {
			t.Error("source/producers changed during G1 capture")
		}
		referenceUnchanged := false
		if reference != nil {
			checkpoint, readErr := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
			next, refErr := guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
			referenceUnchanged = readErr == nil && refErr == nil && reflect.DeepEqual(reference, next)
			if !referenceUnchanged {
				t.Error("original15 corpus changed")
			}
		}
		status := "pass"
		if t.Failed() || len(records) != len(cases) {
			status = "fail"
		}
		manifest := map[string]any{"schema": 1, "kind": "historical-native-G1-registered-GET-walk", "status": status, "historical_producer": guiPublicGetHistory, "source_inputs_sha256": guiPublicGetSourceSHA, "case_registry_sha256": guiPublicGetCasesSHA, "cases": records, "expected_cases": len(cases), "inputs_before": before, "inputs_after": after, "inputs_unchanged": unchanged, "original15_reference_verified": reference != nil && referenceUnchanged, "normalization": "NOT RUN; raw M0 source-native oracle capture is not strict golden/Live acceptance", "scope": "Synthetic anonymous public/SEO GET only; series are real login redirects; no owner-signed group/template-conditional coverage, original14 rewrite, current nonce-tree provenance, auth/privacy/provider/browser/clinical/device/full/retirement/adoption claim"}
		raw, marshalErr := json.MarshalIndent(manifest, "", "  ")
		if marshalErr != nil || guiPublicWrite(output, "manifest.json", append(raw, '\n')) != nil {
			t.Error("G1 capture manifest could not be retained")
		}
	})
	snapshotRoot := guiPublicGetMaterialize(t, files)
	fixtures := map[string]guiPublicGetFixture{}
	for _, state := range []string{"empty", "populated"} {
		fixtures[state] = guiPublicGetFixtureFor(t, snapshotRoot, state)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			fixture := fixtures[c.State]
			c.Path = strings.NewReplacer("{place}", fmt.Sprint(fixture.place), "{event}", fmt.Sprint(fixture.event)).Replace(c.Path)
			s := fixture.server
			s.Renderer = NewRenderer(snapshotRoot) // Fresh native cache/load trace per GET.
			trace := &guiPublicTraceLoader{delegate: s.Renderer.loader}
			s.Renderer.set = pongo2.NewSet("native-social-original-G1", trace)
			mux := http.NewServeMux()
			s.Register(mux)
			r := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+c.Path, nil)
			r.AddCookie(&http.Cookie{Name: "django_language", Value: c.Language})
			r.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("g", 52)})
			if c.Profile == "contrast" {
				for key, value := range map[string]string{"display_theme": "contrast", "display_text": "larger", "display_motion": "reduce"} {
					r.AddCookie(&http.Cookie{Name: key, Value: value})
				}
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			canonical := s.Config.PublicURL + r.URL.Path
			if c.Route == "place_detail" || c.Route == "place_detail_slug" {
				canonical = fmt.Sprintf("%s/places/%d/original-synthetic-hall/", s.Config.PublicURL, fixture.place)
			}
			if c.Route == "event_detail" || c.Route == "event_detail_slug" {
				canonical = fmt.Sprintf("%s/events/%d/original-synthetic-event/", s.Config.PublicURL, fixture.event)
			}
			body := append([]byte(nil), w.Body.Bytes()...)
			name := c.ID + ".body"
			if err := guiPublicWrite(output, name, body); err != nil {
				t.Fatal("raw HTTP body retention failed")
			}
			checkErr := guiPublicGetCheck(c, w, trace.loads, canonical)
			if checkErr == nil {
				for _, load := range trace.loads {
					path := "apps/web/templates/" + load.Name
					if load.Name == "base.html" {
						path = "templates/base.html"
					}
					raw, exists := files[path]
					if !exists || load.SHA256 != guiPublicHash([]byte(transformTemplate(string(raw)))) {
						checkErr = fmt.Errorf("historical-template-stream")
						break
					}
				}
			}
			if checkErr == nil && c.ID == "donate-selected" && !bytes.Contains(body, []byte(fmt.Sprintf(`value="%d" selected`, fixture.campaign))) {
				checkErr = fmt.Errorf("campaign-selection")
			}
			code := ""
			if checkErr != nil {
				code = checkErr.Error() // Closed local field codes only, never body/error text.
			}
			headers := map[string]string{}
			for _, name := range []string{"Content-Type", "Cache-Control", "Location", "Vary"} {
				headers[name] = w.Header().Get(name)
			}
			record := map[string]any{"id": c.ID, "route": c.Route, "method": "GET", "path": c.Path, "request_actor": "anonymous", "language": c.Language, "profile": c.Profile, "state": c.State, "expected_status": c.Status, "actual_status": w.Code, "response_headers": headers, "expected_template": c.Template, "template_loads": trace.loads, "body_file": name, "body_bytes": len(body), "body_sha256": guiPublicHash(body), "http_assertions_pass": checkErr == nil, "failure_code": code, "mutation_controls": []string{}}
			records = append(records, record)
			if checkErr != nil {
				t.Fatalf("registered GET refused: %s", code)
			}
			if c.ID == "landing-en-default" {
				mutations := []struct {
					code  string
					apply func(*httptest.ResponseRecorder, *[]guiPublicTemplateLoad)
				}{
					{"status", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) { v.Code = 500 }},
					{"content-type", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Header().Set("Content-Type", "application/json")
					}},
					{"cache", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Header().Set("Cache-Control", "public")
					}},
					{"template", func(_ *httptest.ResponseRecorder, loads *[]guiPublicTemplateLoad) {
						(*loads)[0].Name = "web/terms.html"
					}},
					{"language", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Body = bytes.NewBuffer(bytes.Replace(v.Body.Bytes(), []byte(`lang="en"`), []byte(`lang="ro"`), 1))
					}},
					{"canonical", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Body = bytes.NewBuffer(bytes.Replace(v.Body.Bytes(), []byte(`<link rel="canonical" href="`+canonical+`">`), []byte(`<link rel="canonical" href="https://fixture.invalid/wrong/">`), 1))
					}},
					{"robots", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Body = bytes.NewBuffer(bytes.Replace(v.Body.Bytes(), []byte(`name="robots" content="index, follow"`), []byte(`name="robots" content="noindex, follow"`), 1))
					}},
					{"heading", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Body = bytes.NewBuffer(bytes.Replace(v.Body.Bytes(), []byte("<h1>"), []byte("<div>"), 1))
					}},
					{"footer", func(v *httptest.ResponseRecorder, _ *[]guiPublicTemplateLoad) {
						v.Body = bytes.NewBuffer(bytes.ReplaceAll(v.Body.Bytes(), []byte(`href="/privacy/"`), []byte(`href="/missing-synthetic/"`)))
					}},
				}
				passed := []string{}
				for _, mutation := range mutations {
					t.Run("refuse-"+mutation.code, func(t *testing.T) {
						v := httptest.NewRecorder()
						v.Code, v.HeaderMap, v.Body = w.Code, w.Header().Clone(), bytes.NewBuffer(append([]byte(nil), body...))
						loads := append([]guiPublicTemplateLoad(nil), trace.loads...)
						mutation.apply(v, &loads)
						if got := guiPublicGetCheck(c, v, loads, canonical); got == nil || got.Error() != mutation.code {
							t.Fatal("mutation was not refused by its owning assertion")
						}
						passed = append(passed, mutation.code)
					})
				}
				record["mutation_controls"] = passed
			}
		})
	}
}

func TestGUIPublicGetWalkSourceRefusals(t *testing.T) {
	var original map[string]any
	if json.Unmarshal(guiPublicGetSourceBytes, &original) != nil {
		t.Fatal("source fixture JSON invalid")
	}
	for _, name := range []string{"wrong-history", "wrong-source-hash", "duplicate-path", "alias-path", "missing-base"} {
		t.Run(name, func(t *testing.T) {
			var candidate map[string]any
			if json.Unmarshal(guiPublicGetSourceBytes, &candidate) != nil {
				t.Fatal("source control decoding failed")
			}
			rows := candidate["files"].([]any)
			switch name {
			case "wrong-history":
				candidate["source_commit"] = strings.Repeat("0", 40)
			case "wrong-source-hash":
				rows[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			case "duplicate-path":
				rows[1] = rows[0]
			case "alias-path":
				rows[0].(map[string]any)["path"] = "templates/../base.html"
			case "missing-base":
				for _, row := range rows {
					if row.(map[string]any)["path"] == "templates/base.html" {
						row.(map[string]any)["path"] = "templates/missing-base.html"
					}
				}
			}
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal("source control encoding failed")
			}
			if _, _, err := guiPublicGetSources(raw); err == nil {
				t.Fatal("source control was accepted")
			}
		})
	}
	t.Run("missing-dsn", func(t *testing.T) {
		if guiPublicGetRequireDSN("") == nil || guiPublicGetRequireDSN("explicit-synthetic-fixture-placeholder") != nil {
			t.Fatal("capture prerequisite refusal changed")
		}
	})
	// Source-only controls use owned in-memory copies of actual physical inputs;
	// no historical file, producer or raw receiver input is replaced on disk.
	root, err := filepath.Abs("../../../..")
	source, _, sourceErr := guiPublicGetSources(guiPublicGetSourceBytes)
	inputs, inputErr := guiPublicGetRepairInputs(root, source)
	if err != nil || sourceErr != nil || inputErr != nil || guiPublicGetRepairHistoricalProof(source, inputs) != nil {
		t.Fatal("reviewed current repair profile unavailable for source controls")
	}
	for _, name := range []string{"profile", "unknown", "mixed", "mode", "missing", "preserved-source", "producer-source", "history", "capture-boundary"} {
		t.Run("repair-"+name, func(t *testing.T) {
			changed := map[string]guiPublicGetRepairInput{}
			for path, input := range inputs {
				changed[path] = guiPublicGetRepairInput{append([]byte(nil), input.raw...), input.binding}
			}
			encoded, err := json.Marshal(source)
			var candidate guiPublicGetSource
			if err != nil || json.Unmarshal(encoded, &candidate) != nil {
				t.Fatal("owned historical source control unavailable")
			}
			replace := func(path string, raw []byte) {
				input := changed[path]
				input.raw, input.binding.Bytes, input.binding.SHA256 = raw, len(raw), guiPublicHash(raw)
				changed[path] = input
			}
			switch name {
			case "profile":
				if guiPublicGetBindRequestedProducers(root, source, false) != nil {
					t.Fatal("complete reviewed default profile refused")
				}
				return
			case "unknown":
				path := "services/server/internal/web/renderer.go"
				replace(path, append(changed[path].raw, '\n'))
			case "mixed":
				row := guiPublicGetRepairRows()[0]
				replace(row.path, bytes.Replace(changed[row.path].raw, []byte(row.after), []byte(row.before), 1))
			case "mode":
				path := "services/server/internal/web/server.go"
				input := changed[path]
				input.binding.FullMode = 0o100644
				changed[path] = input
			case "missing":
				delete(changed, "services/server/internal/web/server.go")
			case "preserved-source":
				path := source.PreservedSourceInputs[0].Path
				replace(path, append(changed[path].raw, '\n'))
			case "producer-source":
				path := "services/server/internal/web/router.go"
				replace(path, append(changed[path].raw, '\n'))
			case "history":
				for index := range candidate.Producers {
					if candidate.Producers[index].Path == "services/server/internal/web/renderer.go" {
						candidate.Producers[index].HistoricalSHA256 = strings.Repeat("0", 64)
					}
				}
			case "capture-boundary":
				if guiPublicGetBindRequestedProducers(root, source, true) == nil || guiPublicGetBindProducers(root, source) == nil {
					t.Fatal("repaired physical producer admitted as old HTTP capture")
				}
				return
			}
			if guiPublicGetRepairHistoricalProof(candidate, changed) == nil || guiPublicGetRepairHistoricalProof(source, inputs) != nil {
				t.Fatal("changed repair source admitted or source control aliased")
			}
		})
	}
}
