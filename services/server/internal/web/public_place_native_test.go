package web

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func publicPlaceNativeFixture(t *testing.T) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	r, prior, data := topicsNativeFixture(t)
	actor, _ := platform.ActorFrom(prior)
	request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://topics.example/places/9007199254740993/", nil), actor)
	data["request"] = map[string]any{"path": request.URL.Path, "csp_nonce": "synthetic-public-place-nonce"}
	data["canonical_url"], data["og_locale"] = request.URL.String(), "en_GB"
	data["place_label"] = "Generated <venue> & location"
	data["place"] = map[string]any{"id": int64(9007199254740993), "pk": int64(9007199254740993), "display_address": "Generated address", "address_country": "RO", "website": "https://venue.example/", "posted_hours_text": "Mo-Fr 09:00-18:00", "display_opening_hours": map[string]any{"mon": "09:00-18:00"}, "_display_name": "Generated <venue> & location", "_seed_hint": "generated", "get_source_display": "OpenStreetMap", "local_poc": false, "_tags": map[string]any{"PRIVATE_public_proof": "PRIVATE-do-not-render"}}
	data["place_visual"] = map[string]any{"kind": "accent", "svg": activitySVG("place:generated:Generated <venue> & location")}
	data["category_chips"] = []map[string]any{{"name": "Second supplied first"}, {"name": "First supplied second"}}
	data["open_now"], data["has_access_pref"], data["access_match"] = "unverified", true, "unknown"
	data["can_contribute"], data["has_kid_facts"] = false, false
	data["access_facts"] = []map[string]any{{"key": "step_free", "label": "Step-free access", "state": "unknown"}, {"key": "hearing_loop", "label": "Hearing loop", "state": "limited"}}
	data["recorded_access_facts"] = []map[string]any{{"key": "hearing_loop", "label": "Hearing loop", "state": "limited"}}
	data["venue_facts"] = []map[string]any{{"key": "toilets", "label": "Toilets", "state": "unknown", "osm_sourced": false, "yes": 1, "no": 2, "required": 3, "my_vote": nil}, {"key": "playground", "label": "Playground", "state": "true", "osm_sourced": true, "yes": 0, "no": 0, "required": 3, "my_vote": nil}}
	data["recorded_venue_facts"] = []map[string]any{{"key": "playground", "label": "Playground", "state": "true"}}
	data["place_brief"] = [][]any{{"Where", "A supplied sentence."}}
	data["edges"] = []map[string]any{{"id": int64(9007199254740992), "activity": map[string]any{"name": "Generated activity"}, "is_disputed": false, "summary": map[string]any{"is_confirmed": false, "confirms": 1, "required": 3, "disputes": 2, "my_vote": "confirm"}}}
	data["corrections"] = []map[string]any{{"id": 94, "field_label": "Name", "proposed_value": "<script>private-correction</script>", "confirms": 2, "required": 3, "is_proposer": false, "confirmed_by_me": true}}
	data["meetups"], data["events"], data["share_targets"] = []map[string]any{}, []map[string]any{}, []map[string]any{}
	data["attribution_credit"] = map[string]any{"attribution": "Generated credit", "license_name": "ODbL", "provenance_url": "https://source.example/place"}
	data["related_landings"] = []map[string]any{{"name": "Generated topic", "url": "/things-to-do/generated/topic/"}}
	data["related_city"] = map[string]any{"name": "Generated city", "url": "/things-to-do/generated/"}
	return r, request, data
}

func publicPlaceNativeRender(t *testing.T, r *Renderer, request *http.Request, data pongo2.Context) string {
	t.Helper()
	raw, err := r.renderPublicNative(request, "web/place_detail.html", data)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNativePublicPlacePreservesUnknownFactsCountsAndEscaping(t *testing.T) {
	for _, language := range []string{"en", "ro"} {
		r, request, data := publicPlaceNativeFixture(t)
		data["language"] = language
		before, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		body := publicPlaceNativeRender(t, r, request, data)
		after, _ := json.Marshal(data)
		if !bytes.Equal(before, after) {
			t.Fatal("projection mutated the producer")
		}
		for _, fragment := range []string{"Generated &lt;venue&gt; &amp; location", "Generated credit", "ODbL", "https://source.example/place", "A supplied sentence.", "1/3", "1 yes / 2 no", "9007199254740993", "&lt;script&gt;private-correction&lt;/script&gt;"} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("missing selected public value %q", fragment)
			}
		}
		if strings.Index(body, "Second supplied first") > strings.Index(body, "First supplied second") || strings.Contains(body, "PRIVATE-do-not-render") || strings.Contains(body, "<script>private-correction") || strings.Contains(body, `action="/places/9007199254740993/fact`) {
			t.Fatal("order, privacy, escape or contribution boundary differs")
		}
		page, err := r.publicPlaceProjection(request, data)
		if err != nil || page.AccessClass != "" || page.Venue[0].State != "unknown" || page.Venue[0].VotedNo || page.Venue[0].VotedYes {
			t.Fatal("unknown accessibility or absent vote became a claim", err)
		}
	}
}

func TestNativePublicPlaceCapabilityAndVoteActions(t *testing.T) {
	for _, contribute := range []bool{false, true} {
		for _, staff := range []bool{false, true} {
			r, request, data := publicPlaceNativeFixture(t)
			actor, _ := platform.ActorFrom(request)
			actor.IsStaff = staff
			request = platform.WithActor(request, actor)
			data["user"].(map[string]any)["is_staff"] = staff
			data["can_contribute"] = contribute
			data["venue_facts"].([]map[string]any)[0]["my_vote"] = false
			body := publicPlaceNativeRender(t, r, request, data)
			for _, route := range []string{"edge_vote", "fact_vote", "place_correction_confirm", "place_correction_propose", "place_open_now_report", "place_closure_report"} {
				args := []any{int64(9007199254740993)}
				if route == "edge_vote" {
					args = append(args, int64(9007199254740992))
				}
				if route == "place_correction_confirm" {
					args = append(args, 94)
				}
				if strings.Contains(body, `action="`+routeURL(route, args...)+`"`) != contribute {
					t.Fatalf("%s capability differs", route)
				}
			}
			for _, route := range []string{"place_open_now_reset", "place_closure_reset"} {
				if strings.Contains(body, `action="`+routeURL(route, int64(9007199254740993))+`"`) != staff {
					t.Fatalf("%s staff control differs", route)
				}
			}
			if contribute && (!strings.Contains(body, `name="vote" value="confirm" disabled`) || !strings.Contains(body, `name="value" value="no" disabled`) || !strings.Contains(body, `maxlength="255" required`)) {
				t.Fatal("existing selections or correction constraints lost")
			}
		}
	}
}

func TestNativePublicPlaceAnonymousAndSignedEventScope(t *testing.T) {
	for _, signed := range []bool{false, true} {
		r, request, data := publicPlaceNativeFixture(t)
		if !signed {
			request = httptest.NewRequest(http.MethodGet, request.URL.String(), nil)
			data["user"] = map[string]any{"is_authenticated": false}
		}
		data["events"] = []map[string]any{{"id": 1, "title": "POC past supplied event", "starts_at": time.Date(2026, 1, 12, 13, 0, 0, 0, time.UTC), "local_poc": true, "local_poc_past": true}}
		data["local_poc_mode"], data["local_poc_past"] = true, false
		body := publicPlaceNativeRender(t, r, request, data)
		if strings.Contains(body, "POC past supplied event") != signed || strings.Contains(body, `href="?include_past=1"`) != signed || strings.Contains(body, `href="/login/">Log in</a> or`) == signed {
			t.Fatal("signed-only activity/event section differs")
		}
	}
}

func TestNativePublicPlaceUsesActualHydratedActivityShape(t *testing.T) {
	r, request, data := publicPlaceNativeFixture(t)
	data["meetups"] = []map[string]any{{"id": 97, "title": "Hydrated meetup", "starts_at": time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC), "activity_type": "chess", "activity_type_obj": map[string]any{"id": 14, "name": "Chess", "slug": "chess"}, "place": 11, "place_obj": map[string]any{"id": 11, "display_name": "Hydrated place", "address_city": "Generated city"}, "secondary_types": []any{map[string]any{"id": 15, "name": "Board games", "slug": "board-games"}}, "description": "Supplied activity description.", "cost_band": "free", "difficulty": "easy", "status": "open", "guardian_accompanied": true, "beginners_welcome": true}}
	before, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	body := publicPlaceNativeRender(t, r, request, data)
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatal("hydrated model aliases mutated the producer")
	}
	for _, text := range []string{"Hydrated meetup", "Chess", "Board games", "Hydrated place", "Generated city", "Free", "Easy", "guardian-accompanied"} {
		if !strings.Contains(body, text) {
			t.Fatalf("hydrated presentation missing %q", text)
		}
	}
}

func TestNativePublicPlaceNullableCatalogAndPendingVotes(t *testing.T) {
	yes, no := true, false
	for _, vote := range []any{nil, (*bool)(nil), &yes, &no, true, false} {
		r, request, data := publicPlaceNativeFixture(t)
		data["venue_facts"].([]map[string]any)[0]["my_vote"] = vote
		p, err := r.publicPlaceProjection(request, data)
		if err != nil {
			t.Fatal("legitimate producer vote rejected", err)
		}
		wantYes, wantNo := false, false
		switch value := vote.(type) {
		case *bool:
			if value != nil {
				wantYes, wantNo = *value, !*value
			}
		case bool:
			wantYes, wantNo = value, !value
		}
		if p.Venue[0].VotedYes != wantYes || p.Venue[0].VotedNo != wantNo {
			t.Fatal("nullable vote changed")
		}
	}
}

func TestNativePublicPlacePendingSourceCreditAndSafeVisuals(t *testing.T) {
	r, request, data := publicPlaceNativeFixture(t)
	data["pending_proposal"] = map[string]any{"id": 92, "confirmations_count": 2, "required_confirmations": 4}
	data["place_visual"] = map[string]any{"kind": "place_cover_photo", "url": "/api/media/place-cover-file/generated/", "alt": "Generated cover", "attribution": "Visual credit", "license_name": "CC-BY", "source_page_url": "https://commons.example/image"}
	data["partner"] = map[string]any{"name": "Generated partner", "website": "javascript:alert(1)", "get_kind_display": "Business", "blurb": "<img onerror=alert(1)>"}
	body := publicPlaceNativeRender(t, r, request, data)
	if !strings.Contains(body, "2 of 4") || !strings.Contains(body, `src="/api/media/place-cover-file/generated/"`) || !strings.Contains(body, "Visual credit") || !strings.Contains(body, "CC-BY") || strings.Contains(body, "javascript:") || strings.Contains(body, "<img onerror") {
		t.Fatal("pending count, licensed cover or partner escaping differs")
	}
	data["place_visual"].(map[string]any)["url"] = "https://untrusted.example/cover.jpg"
	if _, err := r.publicPlaceProjection(request, data); err == nil {
		t.Fatal("non-media cover accepted")
	}
	data["place_visual"] = map[string]any{"kind": "accent", "svg": pongo2.AsSafeValue(`<svg onload="alert(1)"></svg>`)}
	if _, err := r.publicPlaceProjection(request, data); err == nil {
		t.Fatal("arbitrary trusted SVG accepted")
	}
}

func TestNativePublicPlaceOfficialImageFormAndConcurrentRendering(t *testing.T) {
	r, request, data := publicPlaceNativeFixture(t)
	actor, _ := platform.ActorFrom(request)
	form, err := (&Server{}).form(request, actor, "PlaceOfficialImageForm", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	data["official_claim"], data["official_image_form"] = map[string]any{"id": 1}, form
	data["place"].(map[string]any)["cover"] = true
	body := publicPlaceNativeRender(t, r, request, data)
	for _, fragment := range []string{`enctype="multipart/form-data"`, `type="file" name="image" required`, `name="alt_text" value="" maxlength="140"`, `type="checkbox" name="rights_confirmed" value="on" required`, `name="remove" value="1"`} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("official image constraint missing: %q", fragment)
		}
	}
	if strings.Contains(body, `type="file" name="image" value=`) {
		t.Fatal("file control exposed a value")
	}
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			raw, err := r.renderPublicNative(request, "web/place_detail.html", data)
			if err == nil && string(raw) != body {
				err = http.ErrAbortHandler
			}
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("cached template shared mutable state", err)
		}
	}
	if !strings.Contains(body, html.EscapeString("Generated <venue> & location")) {
		t.Fatal("lost heading")
	}
}
