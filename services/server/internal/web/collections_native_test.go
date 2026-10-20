package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func collectionsNativeFixture(t *testing.T, venues bool) (*Renderer, *http.Request, pongo2.Context, string) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	actor := platform.Actor{ID: 961, PublicID: "55555555-5555-4555-8555-555555555961", Username: "generated-collections", DisplayName: "Generated <person> & user", Cohort: "adult", AgeBand: "adult", IsActive: true}
	route, name := "my_meetups", "web/my_meetups.html"
	if venues {
		route, name = "my_venues", "web/my_venues.html"
	}
	path := routeURL(route)
	request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://collections.example"+path+"?existing=1", nil), actor)
	user := socialActor(actor)
	data := pongo2.Context{
		"user": user, "language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated collections site", "canonical_url": "https://collections.example" + path, "csrf": "synthetic-collections-csrf",
		"request": map[string]any{"path": path, "csp_nonce": "synthetic-collections-nonce"}, "connections_enabled": false, "unread_notifications": 3,
		"PRIVATE_unrendered": "PRIVATE-collections-unrendered",
	}
	stamp := time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC)
	if venues {
		data["venues"] = []map[string]any{
			{"place": map[string]any{"pk": int64(9007199254740993), "display_name": "Zed supplied first"}, "flags": []string{"correction_pending", "closed", "unrecognized-private-flag", "hours_unverified", "closed"}},
			{"place": map[string]any{"pk": 72, "display_name": "Alpha supplied second"}, "flags": []any{"correction_pending"}},
		}
	} else {
		data["generated_at"] = stamp
		data["meetups"] = []map[string]any{
			{"pk": int64(9007199254740993), "title": "Zed supplied first", "activity_type": map[string]any{"name": "Generated kind"}, "starts_at": stamp, "ends_at": stamp.Add(time.Hour), "place": map[string]any{"display_name": "Supplied hall", "name": "Unselected fallback"}, "meeting_point": "North gate\nSecond line"},
			{"pk": 72, "title": "Alpha supplied second", "activity_type": map[string]any{"name": "Second supplied kind"}, "starts_at": stamp.Add(24 * time.Hour), "ends_at": nil, "place": nil, "meeting_point": ""},
		}
		data["my_guardians"] = []map[string]any{{"display_name": "", "username": "Supplied fallback guardian"}, {"display_name": "Supplied guardian", "username": "Unselected guardian handle"}}
	}
	return NewRenderer(root), request, data, name
}

func TestNativeCollectionsPreservesMeetupOrderDatesAndConditionalDetails(t *testing.T) {
	r, request, data, name := collectionsNativeFixture(t, false)
	before, _ := json.Marshal(data)
	page, err := r.collectionsPage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) || page.Kind != "meetups" || len(page.Meetups) != 2 || page.Meetups[0].Title != "Zed supplied first" || page.Meetups[1].Title != "Alpha supplied second" {
		t.Fatal("input mutation or meetup order changed")
	}
	stamp := data["generated_at"].(time.Time)
	if page.Meetups[0].Start != djangoDate(stamp, "D d M, H:i") || page.Meetups[0].End != djangoDate(stamp.Add(time.Hour), "H:i") || page.Meetups[0].URL != routeURL("activity_detail", int64(9007199254740993)) || page.CalendarURL != routeURL("my_calendar") {
		t.Fatal("date, exact integer identity or named download destination changed")
	}
	if page.Meetups[1].End != "" || page.Meetups[1].Place != "" || page.Meetups[0].Place != "Supplied hall" || len(page.Guardians) != 2 || page.Guardians[0] != "Supplied fallback guardian" {
		t.Fatal("optional fields or supplied guardian fallback changed")
	}
	raw, err := r.renderCollections(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Index(body, "Zed supplied first") > strings.Index(body, "Alpha supplied second") || !strings.Contains(body, ` download>`) || !strings.Contains(body, `id="offline-note" class="card card-accent" hidden role="status"`) || !strings.Contains(body, `src="/static/js/my-meetups.js" defer`) || !strings.Contains(body, "North gate\nSecond line") {
		t.Fatal("calendar, existing offline hook or no-JS content lost")
	}
	for _, private := range []string{"PRIVATE-collections-unrendered", "Unselected fallback", "Unselected guardian handle", "cohort", "owner_id"} {
		if strings.Contains(body, private) {
			t.Fatal("unselected or raw private field rendered")
		}
	}
	data["meetups"].([]map[string]any)[0]["title"] = "mutated"
	data["my_guardians"].([]map[string]any)[0]["username"] = "mutated"
	if page.Meetups[0].Title != "Zed supplied first" || page.Guardians[0] != "Supplied fallback guardian" {
		t.Fatal("typed output retained mutable input")
	}
}

func TestNativeCollectionsPreservesVenueFlagOrderWithoutRecomputingPolicy(t *testing.T) {
	r, request, data, name := collectionsNativeFixture(t, true)
	before, _ := json.Marshal(data)
	page, err := r.collectionsPage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) || len(page.Venues) != 2 || page.Venues[0].Name != "Zed supplied first" || page.Venues[0].URL != routeURL("place_detail", int64(9007199254740993)) || len(page.Venues[0].Flags) != 4 {
		t.Fatal("venue order/identity or supplied flag filter changed")
	}
	want := []collectionsFlag{{"a correction is pending", false}, {"reported closed", true}, {"posted hours unverified", false}, {"reported closed", true}}
	for i, v := range want {
		if page.Venues[0].Flags[i] != v {
			t.Fatal("flag order, duplicates or warning class changed")
		}
	}
	raw, err := r.renderCollections(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "unrecognized-private-flag") || strings.Contains(body, "my-meetups.js") || strings.Contains(body, "offline-note") || strings.Contains(body, "PRIVATE-collections-unrendered") {
		t.Fatal("unknown flags, meetup-only hooks or raw fields rendered")
	}
	data["venues"].([]map[string]any)[0]["flags"].([]string)[0] = "mutated"
	if page.Venues[0].Flags[0].Text != "a correction is pending" {
		t.Fatal("flag output retained mutable input")
	}
	// The admitted SQL display-name fallback may legitimately be blank.
	// Keep the row/link/flags, give its link a generic accessible label, and
	// never mutate or invent a name in the supplied snapshot.
	r, request, data, name = collectionsNativeFixture(t, true)
	data["venues"].([]map[string]any)[0]["place"].(map[string]any)["display_name"] = ""
	blankBefore, _ := json.Marshal(data)
	blank, err := r.collectionsPage(request, name, data)
	if err != nil {
		t.Fatal("legitimate blank venue label refused", err)
	}
	blankAfter, _ := json.Marshal(data)
	if !bytes.Equal(blankBefore, blankAfter) || len(blank.Venues) != 2 || blank.Venues[0].Name != blank.Copy["Place"] || blank.Venues[0].URL != routeURL("place_detail", int64(9007199254740993)) || len(blank.Venues[0].Flags) != 4 || blank.Venues[1].Name != "Alpha supplied second" {
		t.Fatal("blank-label fallback lost identity, flags, order or producer bytes")
	}
	raw, err = r.renderCollections(request, name, data)
	if err != nil || !strings.Contains(string(raw), `href="`+blank.Venues[0].URL+`">Place</a>`) {
		t.Fatal("blank venue needs the existing generic accessible Place link")
	}
}

func TestNativeCollectionsEmptyAndParticipationRefusedSnapshotsStayReadOnly(t *testing.T) {
	for _, venues := range []bool{false, true} {
		r, request, data, name := collectionsNativeFixture(t, venues)
		if venues {
			data["venues"] = []map[string]any{}
		} else {
			data["meetups"] = []map[string]any{}
			data["my_guardians"] = nil
		}
		// Existing producers may supply empty lists after Participate refuses.
		// Rendering must not add a new active/age/cohort authority decision.
		actor, _ := platform.ActorFrom(request)
		actor.Cohort = "unassigned"
		actor.IdentityVerified = false
		actor.AgeBand = ""
		request = platform.WithActor(request, actor)
		data["user"] = socialActor(actor)
		raw, err := r.renderCollections(request, name, data)
		if err != nil {
			t.Fatal("valid signed empty snapshot tightened", err)
		}
		actor.IsActive = false
		inactive := platform.WithActor(request, actor)
		if output, err := r.renderCollections(inactive, name, data); !errors.Is(err, platform.ErrForbidden) || len(output) != 0 {
			t.Fatal("inactive actor bypassed common authentication")
		}
		body := string(raw)
		if strings.Contains(body, ` download>`) || strings.Contains(body, "Grown-ups you can turn to") || strings.Contains(body, "collection-meetup\"") {
			t.Fatal("empty-only control changed")
		}
		if venues && !strings.Contains(body, "Nothing to flag") || !venues && (!strings.Contains(body, "No upcoming meetups yet.") || !strings.Contains(body, `id="offline-note"`)) {
			t.Fatal("empty fallback or preserved offline notice lost")
		}
		if strings.Count(body, `<form method="post"`) != 2 || strings.Contains(body, `name="user_id"`) {
			t.Fatal("new self-list mutation form or subject field appeared")
		}
	}
}

func TestNativeCollectionsEscapingShellAndSparseDefaultIdentity(t *testing.T) {
	for _, venues := range []bool{false, true} {
		r, request, data, name := collectionsNativeFixture(t, venues)
		actor, _ := platform.ActorFrom(request)
		data["user"] = map[string]any{"is_authenticated": true, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName}
		data["messages"] = []socialMessage{{"message": "<script>notice</script>", "tags": "error\" onclick=payload"}}
		data["connections_enabled"] = true
		data["language"] = "ro"
		if venues {
			data["venues"].([]map[string]any)[0]["place"].(map[string]any)["display_name"] = "<script>place</script>"
		} else {
			data["meetups"].([]map[string]any)[0]["title"] = "<script>title</script>"
			data["meetups"].([]map[string]any)[0]["meeting_point"] = "<img src=x onerror=payload>\nSecond line"
		}
		raw, err := r.renderCollections(request, name, data)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if !strings.Contains(body, `<html lang="ro"`) || !strings.Contains(body, `href="/connections/"`) || !strings.Contains(body, "&lt;script&gt;notice&lt;/script&gt;") || !strings.Contains(body, "Generated &lt;person&gt; &amp; user") {
			t.Fatal("resolved shell or escaping lost")
		}
		for _, bad := range []string{"<script>notice", "<script>place", "<script>title", "<img src=x", `" onclick=payload`} {
			if strings.Contains(body, bad) {
				t.Fatal("active payload appeared")
			}
		}
		if !strings.Contains(body, `name="next" value="`+request.URL.Path+`"`) || strings.Contains(body, `value="`+request.URL.RequestURI()+`"`) {
			t.Fatal("existing language-switch path contract changed")
		}
	}
}

func TestNativeCollectionsMalformedSnapshotRefusesBeforeOutput(t *testing.T) {
	for key, mutate := range map[string]func(pongo2.Context){
		"missing-list":     func(d pongo2.Context) { delete(d, "meetups") },
		"row-type":         func(d pongo2.Context) { d["meetups"] = []any{"bad"} },
		"bad-id":           func(d pongo2.Context) { d["meetups"].([]map[string]any)[0]["pk"] = "not-an-integer" },
		"nontext-title":    func(d pongo2.Context) { d["meetups"].([]map[string]any)[0]["title"] = true },
		"bad-date":         func(d pongo2.Context) { d["generated_at"] = "not-a-date" },
		"bad-guardians":    func(d pongo2.Context) { d["my_guardians"] = []any{false} },
		"foreign-identity": func(d pongo2.Context) { d["user"].(map[string]any)["public_id"] = "foreign" },
		"foreign-rich-id":  func(d pongo2.Context) { d["user"].(map[string]any)["id"] = 1 },
		"bad-language":     func(d pongo2.Context) { d["language"] = "unknown" },
		"unsafe-canonical": func(d pongo2.Context) { d["canonical_url"] = "javascript:payload" },
		"empty-csrf":       func(d pongo2.Context) { d["csrf"] = "" },
		"wrong-path":       func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/foreign/" },
	} {
		t.Run(key, func(t *testing.T) {
			r, request, data, name := collectionsNativeFixture(t, false)
			mutate(data)
			if raw, err := r.renderCollections(request, name, data); err == nil || len(raw) != 0 {
				t.Fatal("invalid snapshot produced HTML")
			}
		})
	}
	r, request, data, name := collectionsNativeFixture(t, true)
	data["venues"].([]map[string]any)[0]["flags"] = []any{"closed", false}
	if raw, err := r.renderCollections(request, name, data); err == nil || len(raw) != 0 {
		t.Fatal("invalid flag list produced HTML")
	}
	if raw, err := r.renderCollections(httptest.NewRequest(http.MethodGet, "https://collections.example/", nil), name, data); !errors.Is(err, platform.ErrForbidden) || len(raw) != 0 {
		t.Fatal("anonymous direct render admitted")
	}
	r, request, data, _ = collectionsNativeFixture(t, false)
	if raw, err := r.renderCollections(request, "web/saved_searches.html", data); !errors.Is(err, errCollectionsSnapshot) || len(raw) != 0 {
		t.Fatal("unselected page admitted")
	}
}

func TestNativeCollectionsCachedConcurrentRenderingAndFailureNoPartialBytes(t *testing.T) {
	parsed, err := loadCollectionsTemplates()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadCollectionsTemplates()
	if err != nil || parsed != second {
		t.Fatal("template cache is not shared immutable parsed source")
	}
	if _, err := parseCollectionsTemplate("{{"); err == nil {
		t.Fatal("template parse failure accepted")
	}
	broken, err := template.New("collections").Parse(`{{define "collections"}}partial{{.MissingField}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := executeCollectionsTemplate(broken, collectionsPage{}); err == nil || len(raw) != 0 {
		t.Fatal("partial failed template bytes escaped")
	}
	r, request, data, name := collectionsNativeFixture(t, false)
	expected, err := r.renderCollections(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failure := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actual, err := r.renderCollections(request, name, data)
			if err != nil {
				failure <- err
			} else if !bytes.Equal(actual, expected) {
				failure <- errors.New("concurrent body changed")
			}
		}()
	}
	wg.Wait()
	close(failure)
	for err := range failure {
		t.Fatal(err)
	}
}

func TestNativeCollectionsOrdinaryDispatchAndHeaderPolicy(t *testing.T) {
	for _, venues := range []bool{false, true} {
		r, request, data, name := collectionsNativeFixture(t, venues)
		w := httptest.NewRecorder()
		if err := r.Render(w, request, name, data); err != nil {
			t.Fatal(err)
		}
		body := w.Body.String()
		if !strings.Contains(body, `class="collections-native has-tabbar"`) || !strings.Contains(body, `/static/css/collections-native.css`) || w.Header().Get("Cache-Control") != "private, no-cache" || w.Header().Get("Vary") != "Cookie" || w.Header().Get("Content-Security-Policy-Report-Only") == "" || w.Header().Get("Set-Cookie") != "" {
			t.Fatal("selected dispatch, policy or asset boundary changed")
		}
		if !strings.Contains(body, `action="/logout/"`) || !strings.Contains(body, `action="/i18n/setlang/"`) || strings.Contains(body, "spa-bootstrap") {
			t.Fatal("native shell form or unrelated SPA content changed")
		}
	}
}
