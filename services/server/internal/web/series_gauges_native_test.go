package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func seriesGaugeNativeFormFixture(name string, actor platform.Actor) map[string]any {
	snapshot := seriesGaugeFormSnapshot{Name: name, Cohort: actor.Cohort, Staff: actor.IsStaff}
	for _, key := range seriesGaugeFieldNames(name, actor.Cohort, actor.IsStaff) {
		input := seriesGaugeFormInput{Name: key, Value: "Generated <field> & value"}
		switch key {
		case "place", "activity_type":
			input.Value = "2"
			input.HasOptions = true
			input.Options = []option{{"2", "Zed supplied first"}, {"1", "Alpha supplied second"}}
		case "cadence":
			input.Value = "weekly"
			input.HasOptions = true
			input.Options = []option{{"weekly", "Weekly"}, {"biweekly", "Every two weeks"}, {"monthly", "Monthly"}}
		case "coarse_window":
			input.Value = "weekend_daytime"
			input.HasOptions = true
			input.Options = []option{{"weekend_daytime", "Weekend daytime"}, {"weekday_evening", "Weekday evening"}}
		case "cost_band", "difficulty":
			input.Value = "unspecified"
			input.HasOptions = true
			input.Options = []option{{"unspecified", "Not specified"}}
		case "starts_at", "ends_at", "first_starts_at":
			input.Value = "2026-10-21T18:30"
		case "capacity", "min_to_go":
			input.Value = "8"
		case "cost_amount":
			input.Value = "12.50"
		case "beginners_welcome", "supervised":
			input.Value = "on"
			input.Checked = false
		}
		snapshot.Fields = append(snapshot.Fields, input)
	}
	return map[string]any{"native_series_gauges": snapshot, "errors": "", "as_p": pongo2.AsSafeValue("PRIVATE_OPAQUE_FORM_NOT_RENDERED"), "non_field_errors": "", "PRIVATE_raw": "PRIVATE_FORM_ROW"}
}
func seriesGaugeNativeFixture(t *testing.T, name string) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	actor := platform.Actor{ID: 945, PublicID: "55555555-5555-4555-8555-555555555945", Username: "generated-series", DisplayName: "Generated <person>", Cohort: "adult", IsActive: true}
	paths := map[string]string{"web/series_list.html": "/activities/series/", "web/series_detail.html": "/activities/series/501/", "web/series_form.html": "/activities/series/new/", "web/gauges.html": "/gauges/", "web/gauge_detail.html": "/gauges/501/", "web/gauge_form.html": "/gauges/new/", "web/gauge_convert.html": "/gauges/501/convert/"}
	request := platform.WithActor(httptest.NewRequest("GET", "https://series.example"+paths[name], nil), actor)
	user := socialActor(actor)
	user["avatar_uri"] = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle cx="4" cy="4" r="3"/></svg>`))
	stamp := time.Date(2026, 10, 21, 15, 30, 0, 0, time.UTC)
	row := map[string]any{"id": int64(501), "pk": int64(501), "title": "Zed supplied first", "description": "Generated first paragraph\nGenerated second paragraph", "cadence": "weekly", "get_cadence_display": "Weekly", "status": "active", "get_status_display": "Active", "next_starts_at": stamp, "next_instance_note": "Generated heads-up", "owner_id": actor.ID, "activity_type": map[string]any{"name": "Basketball"}, "place": map[string]any{"name": "Library & Hall", "display_name": "Published library name", "address_city": "Generated city"}, "get_coarse_window_display": "Weekend daytime", "PRIVATE_payload": "PRIVATE_SERIES_ROW"}
	second := map[string]any{}
	for k, v := range row {
		second[k] = v
	}
	second["id"], second["pk"], second["title"] = int64(502), int64(502), "Alpha supplied second"
	data := pongo2.Context{"user": user, "language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated series site", "csrf": "synthetic-series-csrf", "canonical_url": "https://series.example" + paths[name], "request": map[string]any{"path": paths[name], "csp_nonce": "synthetic-series-nonce"}, "connections_enabled": true, "unread_notifications": 2, "has_guardians": false, "series": []map[string]any{row, second}, "instances": []map[string]any{{"pk": int64(521), "title": "Generated scheduled meetup", "starts_at": stamp, "get_status_display": "Open", "PRIVATE_logistics": "PRIVATE_INSTANCE_ROW"}}, "gauge": row, "is_owner": true, "ready": false, "remaining": int64(2), "is_proposer": true, "viewer_interested": false, "items": []map[string]any{{"gauge": row, "ready": false, "remaining": int64(2)}, {"gauge": second, "ready": true, "remaining": int64(0)}}, "PRIVATE_context": "PRIVATE_CONTEXT_ROW"}
	switch name {
	case "web/series_detail.html":
		data["series"] = row
		data["next_note_form"] = seriesGaugeNativeFormFixture("NextInstanceNoteForm", actor)
	case "web/series_form.html":
		data["form"] = seriesGaugeNativeFormFixture("SeriesForm", actor)
	case "web/gauge_form.html":
		data["form"] = seriesGaugeNativeFormFixture("GaugeForm", actor)
	case "web/gauge_convert.html":
		data["form"] = seriesGaugeNativeFormFixture("GaugeConvertForm", actor)
	}
	return NewRenderer(root), request, data
}

func TestNativeSeriesGaugesSevenModesOrderAndSourceIsolation(t *testing.T) {
	for name := range nativeSeriesGaugeTemplates {
		r, request, data := seriesGaugeNativeFixture(t, name)
		before, _ := json.Marshal(data)
		raw, err := r.renderSeriesGauges(request, name, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		after, _ := json.Marshal(data)
		if !bytes.Equal(before, after) || !bytes.Contains(raw, []byte(`class="series-gauges-native has-tabbar"`)) || bytes.Contains(raw, []byte("PRIVATE_")) {
			t.Fatal("typed projection changed or exposed source state")
		}
		if !bytes.Contains(raw, []byte(`data-meetups-owner="55555555-5555-4555-8555-555555555945"`)) || !bytes.Contains(raw, []byte(`nonce="synthetic-series-nonce"`)) || !bytes.Contains(raw, []byte(`name="csrfmiddlewaretoken" value="synthetic-series-csrf"`)) {
			t.Fatal("shell/nonce/CSRF ownership differs")
		}
		response := httptest.NewRecorder()
		if err := r.Render(response, request, name, data); err != nil || !bytes.Contains(response.Body.Bytes(), []byte(`class="series-gauges-native has-tabbar"`)) {
			t.Fatal("exact named renderer dispatch missing", name, err)
		}
		if name == "web/series_list.html" && bytes.Index(raw, []byte("Zed supplied first")) > bytes.Index(raw, []byte("Alpha supplied second")) {
			t.Fatal("series order changed")
		}
		if name == "web/gauges.html" && !bytes.Contains(raw, []byte("needs 2 more")) {
			t.Fatal("supplied readiness missing")
		}
	}
	r, request, data := seriesGaugeNativeFixture(t, "web/series_list.html")
	data["series"] = []map[string]any{}
	body, err := r.renderSeriesGauges(request, "web/series_list.html", data)
	if err != nil || !bytes.Contains(body, []byte("You haven&#39;t set up any recurring series yet.")) {
		t.Fatal("empty series state missing", err)
	}
	r, request, data = seriesGaugeNativeFixture(t, "web/gauges.html")
	data["items"] = []map[string]any{}
	body, err = r.renderSeriesGauges(request, "web/gauges.html", data)
	if err != nil || !bytes.Contains(body, []byte("No open gauges right now")) {
		t.Fatal("empty gauges state missing", err)
	}
}
func TestNativeSeriesGaugesSeriesOwnerStatusControls(t *testing.T) {
	for _, status := range []string{"active", "paused", "ended"} {
		for _, owner := range []bool{false, true} {
			r, request, data := seriesGaugeNativeFixture(t, "web/series_detail.html")
			row := data["series"].(map[string]any)
			row["status"] = status
			row["get_status_display"] = status
			data["is_owner"] = owner
			raw, err := r.renderSeriesGauges(request, "web/series_detail.html", data)
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"pause", "resume", "end", "next-note"} {
				want := owner && ((action == "pause" && status == "active") || (action == "resume" && status == "paused") || ((action == "end" || action == "next-note") && status != "ended"))
				if bytes.Contains(raw, []byte(`action="/activities/series/501/`+action+`/"`)) != want {
					t.Fatalf("wrong owner/status control %s/%v/%s", status, owner, action)
				}
			}
			if !bytes.Contains(raw, []byte("Generated scheduled meetup")) {
				t.Fatal("scheduled instances omitted")
			}
		}
	}
}
func TestNativeSeriesGaugesGaugeReadinessAndDecisionCapabilities(t *testing.T) {
	for _, ready := range []bool{false, true} {
		for _, proposer := range []bool{false, true} {
			for _, interested := range []bool{false, true} {
				r, request, data := seriesGaugeNativeFixture(t, "web/gauge_detail.html")
				data["ready"], data["is_proposer"], data["viewer_interested"] = ready, proposer, interested
				g := data["gauge"].(map[string]any)
				g["interested_count"], g["roster"] = 999999, []string{"PRIVATE_SIGNAL_NAMES"}
				body, err := r.renderSeriesGauges(request, "web/gauge_detail.html", data)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(body, []byte("999999")) || bytes.Contains(body, []byte("PRIVATE_SIGNAL")) {
					t.Fatal("raw gauge totals or roster leaked")
				}
				if bytes.Contains(body, []byte(`href="/gauges/501/convert/"`)) != proposer {
					t.Fatal("supplied proposer capability changed")
				}
				if bytes.Contains(body, []byte(`action="/gauges/501/interested/"`)) != (!proposer && !interested) || bytes.Contains(body, []byte(`action="/gauges/501/uninterested/"`)) != (!proposer && interested) {
					t.Fatal("supplied interested control changed")
				}
				if bytes.Contains(body, []byte("Needs 2 more to be ready")) == ready {
					t.Fatal("ready branch did not preserve supplied flag")
				}
			}
		}
	}
}
func TestNativeSeriesGaugesFormFieldsCopyBindingAndEscaping(t *testing.T) {
	for _, pair := range [][2]string{{"web/series_form.html", "SeriesForm"}, {"web/gauge_form.html", "GaugeForm"}, {"web/gauge_convert.html", "GaugeConvertForm"}} {
		r, request, data := seriesGaugeNativeFixture(t, pair[0])
		form := data["form"].(map[string]any)
		form["errors"] = "Generated error <script>bad</script>"
		page, err := r.seriesGaugePage(request, pair[0], data)
		if err != nil {
			t.Fatal(err)
		}
		names := seriesGaugeFieldNames(pair[1], "adult", false)
		if len(page.Form.Fields) != len(names) {
			t.Fatal("field census differs")
		}
		for i, field := range page.Form.Fields {
			if field.Name != names[i] {
				t.Fatal("field order differs")
			}
		}
		snapshot := form["native_series_gauges"].(seriesGaugeFormSnapshot)
		first := snapshot.Fields[0]
		if len(first.Options) > 0 {
			snapshot.Fields[0].Options[0].Label = "MUTATED_OPTION"
		}
		form["native_series_gauges"] = snapshot
		if len(page.Form.Fields[0].Options) > 1 && page.Form.Fields[0].Options[1].Label == "MUTATED_OPTION" {
			t.Fatal("projection retained mutable option slice")
		}
		raw, err := r.renderSeriesGauges(request, pair[0], data)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("<script>bad")) || !bytes.Contains(raw, []byte("Generated error &lt;script&gt;bad&lt;/script&gt;")) || bytes.Contains(raw, []byte("PRIVATE_OPAQUE_FORM")) {
			t.Fatal("form escaped projection/opaque boundary differs")
		}
	}
	r, request, data := seriesGaugeNativeFixture(t, "web/series_detail.html")
	page, err := r.seriesGaugePage(request, "web/series_detail.html", data)
	if err != nil || len(page.NextNote.Fields) != 1 || page.NextNote.Fields[0].Name != "next_instance_note" {
		t.Fatal("next-note form lost", err)
	}
	actor, _ := platform.ActorFrom(request)
	request.Method = http.MethodPost
	request.PostForm = url.Values{"next_instance_note": []string{"Bound </textarea><script>note</script>"}}
	actual, err := (&Server{}).form(request, actor, "NextInstanceNoteForm", map[string]any{"next_instance_note": "Original heads-up"})
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := actual["native_series_gauges"].(seriesGaugeFormSnapshot)
	if !ok || len(metadata.Fields) != 1 || metadata.Fields[0].Name != "next_instance_note" || metadata.Fields[0].Value != "Bound </textarea><script>note</script>" {
		t.Fatal("actual producer metadata lost ordered POST binding")
	}
	data["next_note_form"] = actual
	raw, err := r.renderSeriesGauges(request, "web/series_detail.html", data)
	if err != nil || !bytes.Contains(raw, []byte("Bound &lt;/textarea&gt;&lt;script&gt;note&lt;/script&gt;")) || bytes.Contains(raw, []byte("<script>note")) {
		t.Fatal("actual bound next-note producer/render boundary changed", err)
	}
}
func TestNativeSeriesGaugesMalformedSnapshotsFailBeforeOutput(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		change func(pongo2.Context)
	}{
		{"wrong-user", "web/series_list.html", func(d pongo2.Context) { d["user"].(map[string]any)["id"] = 999 }},
		{"wrong-series-list", "web/series_list.html", func(d pongo2.Context) { d["series"] = true }},
		{"wrong-status", "web/series_detail.html", func(d pongo2.Context) { d["series"].(map[string]any)["status"] = "unknown" }},
		{"wrong-date", "web/series_detail.html", func(d pongo2.Context) { d["series"].(map[string]any)["next_starts_at"] = "not-a-date" }},
		{"wrong-remaining", "web/gauge_detail.html", func(d pongo2.Context) { d["remaining"] = "2" }},
		{"negative-remaining", "web/gauge_detail.html", func(d pongo2.Context) { d["remaining"] = int64(-1) }},
		{"wrong-form", "web/gauge_form.html", func(d pongo2.Context) {
			d["form"].(map[string]any)["native_series_gauges"] = pongo2.AsSafeValue("<select>")
		}},
		{"wrong-field-order", "web/series_form.html", func(d pongo2.Context) {
			f := d["form"].(map[string]any)
			v := f["native_series_gauges"].(seriesGaugeFormSnapshot)
			v.Fields[0], v.Fields[1] = v.Fields[1], v.Fields[0]
			f["native_series_gauges"] = v
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, request, data := seriesGaugeNativeFixture(t, c.mode)
			c.change(data)
			body, err := r.renderSeriesGauges(request, c.mode, data)
			if err == nil || len(body) != 0 {
				t.Fatal("malformed source rendered")
			}
			w := httptest.NewRecorder()
			if err = r.Render(w, request, c.mode, data); err == nil || w.Body.Len() != 0 {
				t.Fatal("partial response on rejected projection")
			}
		})
	}
}
func TestNativeSeriesGaugesCachedConcurrentAndDispatchScope(t *testing.T) {
	r, request, data := seriesGaugeNativeFixture(t, "web/series_detail.html")
	want, err := r.renderSeriesGauges(request, "web/series_detail.html", data)
	if err != nil {
		t.Fatal(err)
	}
	first, err := loadSeriesGaugeTemplates()
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadSeriesGaugeTemplates()
	if err != nil || first != again {
		t.Fatal("template cache not reused")
	}
	var wg sync.WaitGroup
	fail := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := r.renderSeriesGauges(request, "web/series_detail.html", data)
			fail <- err != nil || !bytes.Equal(raw, want)
		}()
	}
	wg.Wait()
	close(fail)
	for bad := range fail {
		if bad {
			t.Fatal("concurrent render changed output")
		}
	}
	if len(nativeSeriesGaugeTemplates) != 7 || nativeSeriesGaugeTemplates["web/group_detail.html"] || nativeSeriesGaugeTemplates["web/you.html"] {
		t.Fatal("dispatch escaped seven exact views")
	}
	if raw, err := r.renderSeriesGauges(request, "web/unknown-series.html", data); err == nil || len(raw) != 0 {
		t.Fatal("unknown mode admitted")
	}
	broken, err := template.New("series-gauges").Parse(`{{define "series-gauges"}}prefix{{.Unknown}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := executeSeriesGaugeTemplate(broken, seriesGaugePage{}); err == nil || len(raw) != 0 {
		t.Fatal("template execution exposed partial bytes")
	}
}

func TestNativeSeriesGaugesOriginalRomanianScheduleAndLocationCopy(t *testing.T) {
	for _, name := range []string{"web/series_list.html", "web/series_detail.html", "web/gauges.html", "web/gauge_detail.html", "web/series_form.html"} {
		r, request, data := seriesGaugeNativeFixture(t, name)
		data["language"] = "ro"
		// Deliberately retain the existing catalogue, rather than teaching a fixture
		// new standalone words that the real Romanian source never translated.
		raw, err := r.renderSeriesGauges(request, name, data)
		if err != nil {
			t.Fatal(err)
		}
		body := html.UnescapeString(string(raw))
		switch name {
		case "web/series_list.html":
			if !strings.Contains(body, "următoarea în jurul ") {
				t.Fatal("original Romanian short schedule phrase lost")
			}
			data["series"] = []map[string]any{}
			raw, err = r.renderSeriesGauges(request, name, data)
			if err != nil || !strings.Contains(html.UnescapeString(string(raw)), "Creezi una?") || !strings.Contains(string(raw), `href="/activities/series/new/"`) {
				t.Fatal("original empty-series create link lost", err)
			}
		case "web/series_detail.html":
			if !strings.Contains(body, "La Published library name.") || !strings.Contains(body, "Următoarea apariție este programată în jurul ") {
				t.Fatal("original Romanian place/long schedule phrases lost")
			}
		case "web/series_form.html":
			if !strings.Contains(body, "Definește o singură dată o întâlnire care se repetă") || strings.Contains(body, "&lt;p class=") {
				t.Fatal("original whole-block Romanian series intro lost or paragraph escaped")
			}
		case "web/gauges.html", "web/gauge_detail.html":
			if !strings.Contains(body, "Basketball la Library & Hall") {
				t.Fatal("existing Romanian at-place phrase lost")
			}
		}
	}
	r, request, data := seriesGaugeNativeFixture(t, "web/gauges.html")
	raw, err := r.renderSeriesGauges(request, "web/gauges.html", data)
	if err != nil || !strings.Contains(string(raw), "ready to start") {
		t.Fatal("list ready label differs from original lowercase msgid", err)
	}
}
