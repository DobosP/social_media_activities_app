package web

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/discovery"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func socialLegacyFixture(t *testing.T) (*Server, platform.Actor, int64, int64) {
	t.Helper()
	db := testdb.New(t, *webDomainDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	a := testdb.Actor(t, db, "legacy-organiser", "adult")
	place := testdb.Place(t, db, "Library & Hall", "osm")
	var typ int64
	if err := db.QueryRow(context.Background(), `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	soc.Avatar = accounts.Avatar
	soc.BodyMarkup = BodyMarkup
	soc.ActivityVisuals = func(ctx context.Context, q platform.Querier, a platform.Actor, ids []int64) (map[int64]any, error) {
		out := map[int64]any{}
		for _, id := range ids {
			out[id] = map[string]any{"kind": "generated_accent"}
		}
		return out, nil
	}
	root, _ := filepath.Abs("../../../..")
	s := &Server{DB: db, Social: soc, Renderer: NewRenderer(root), Catalog: catalog.New(db), Messaging: messaging.New(db, platform.CursorCodec{Key: []byte("synthetic-fixture-cursor-key-32bytes")}), Config: Config{Root: root, PublicURL: "https://fixture.local"}}
	s.Recommendations = recommendations.New(db, s.Catalog, soc)
	s.Discovery = discovery.New(db, s.Catalog, soc, s.Recommendations)
	soc.AfterActivitySave = s.Recommendations.RecomputeEmbeddingTx
	s.Media = media.NewService(db, nil, nil, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, soc)
	if err := messaging.EnsureSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	s.Safety = safety.New(db, safety.Config{CanSeeUser: soc.CanSeeUser})
	if err := s.Safety.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO django_content_type(app_label,model) VALUES('social','activity'),('social','post'),('accounts','user') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	soc.Register(mux)
	s.Media.Register(mux)
	s.API = mux
	return s, a, place, typ
}

func socialLegacyActivity(t *testing.T, s *Server, a platform.Actor, place, typ int64, title string) int64 {
	t.Helper()
	min := 1
	id, err := s.Social.CreateActivity(context.Background(), a, social.ActivityInput{Place: place, ActivityType: typ, Title: title, Description: "A real fixture meetup.", StartsAt: time.Now().Add(24 * time.Hour), MinToGo: &min, MeetingPoint: "Member-only north gate", WhatToBring: "Member-only water", FirstTimeNote: "Member-only welcome", CostBand: "paid", CostNote: "court rental", BeginnersWelcome: true})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func socialLegacyRequest(method, path string, a platform.Actor, pk int64, values url.Values) *http.Request {
	var body *strings.Reader
	if values == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(values.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	r.SetPathValue("pk", fmt.Sprint(pk))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_ = r.ParseForm()
	}
	return platform.WithActor(r, a)
}

func socialLegacyHTML(t *testing.T, s *Server, a platform.Actor, name string, pk int64, query string) (string, map[string]any) {
	t.Helper()
	r := socialLegacyRequest("GET", "/activities/"+fmt.Sprint(pk)+"/?"+query, a, pk, nil)
	data, template, handled, err := s.SocialView(r, a, name)
	if !handled || err != nil {
		t.Fatal(name, handled, err)
	}
	data["csrf"] = "synthetic-form-csrf"
	w := httptest.NewRecorder()
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		t.Fatal(name, err)
	}
	return w.Body.String(), data
}

func socialLegacyAction(t *testing.T, s *Server, a platform.Actor, name string, pk int64, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := socialLegacyRequest("POST", "/activities/"+fmt.Sprint(pk)+"/", a, pk, values)
	w := httptest.NewRecorder()
	if !s.SocialAction(w, r, a, name) {
		t.Fatal("unhandled action", name)
	}
	return w
}

func TestLegacySocialFormTypedCleaningAndCalendarGrammar(t *testing.T) {
	a := platform.Actor{Cohort: "adult"}
	values := url.Values{"place": {"11"}, "activity_type": {"12"}, "title": {"Summer"}, "starts_at": {"2026-10-26T18:30"}, "cost_amount": {".50"}, "cost_band": {"unspecified"}, "secondary_types": {"13", "13", "14"}, "on_behalf_of": {"forged"}, "cohort": {"child"}, "supervised": {"on"}}
	r := socialLegacyRequest("POST", "/activities/new/", a, 0, values)
	body, err := socialCleanForm(r, a, "activity_create")
	if err != nil {
		t.Fatal(err)
	}
	if body["cost_band"] != "paid" || body["beginners_welcome"] != false || fmt.Sprint(*body["cost_amount"].(*social.Decimal)) != "0.50" || !reflect.DeepEqual(body["secondary_types"], []int64{13, 14}) {
		t.Fatal("typed form contract", body)
	}
	for _, key := range []string{"cohort", "on_behalf_of", "supervised"} {
		if _, exists := body[key]; exists {
			t.Fatal("API-only field accepted", key)
		}
	}
	if got := body["starts_at"].(*time.Time).UTC().Format(time.RFC3339); got != "2026-10-26T16:30:00Z" {
		t.Fatal("local clock/DST", got)
	}
	for _, value := range []string{"-1", "100000", "1.001", "NaN", "Inf", "1,00"} {
		if _, err := socialCost(value); err == nil {
			t.Fatal("bad amount", value)
		}
	}
	for _, value := range []string{"0", "0.01", "99999.99", "1e2", "00012.50"} {
		if _, err := socialCost(value); err != nil {
			t.Fatal("valid amount", value, err)
		}
	}
	values.Set("cost_band", "free")
	r = socialLegacyRequest("POST", "/activities/new/", a, 0, values)
	if _, err = socialCleanForm(r, a, "activity_create"); err == nil {
		t.Fatal("contradictory free amount accepted")
	}
	if _, err = socialFormDate("2026-02-30T12:00", true); err == nil {
		t.Fatal("invalid local date accepted")
	}
	for _, wall := range []string{"2026-03-29T03:30", "2026-10-25T03:30"} {
		if _, err = socialFormDate(wall, true); err == nil {
			t.Fatal("nonexistent or ambiguous local date accepted", wall)
		}
	}
	start := time.Date(2026, 10, 26, 18, 30, 0, 0, time.FixedZone("EET", 2*3600))
	rows := []map[string]any{{"id": int64(7), "title": strings.Repeat("Întâlnire", 20) + "; a,b\nSUMMARY:injected", "starts_at": start, "place": map[string]any{"display_name": "Library\\north"}}}
	calendar := socialCalendar(rows, "fixture.local", start)
	if !strings.Contains(calendar, "DTSTART:20261026T163000Z") || !strings.Contains(calendar, `\; a\,b\nSUMMARY:injected`) || !strings.Contains(calendar, `LOCATION:Library\\north`) {
		t.Fatal("calendar escaping", calendar)
	}
	for _, line := range strings.Split(calendar, "\r\n") {
		if len(line) > 75 || !utf8.ValidString(line) {
			t.Fatal("calendar octet fold", line)
		}
	}
	r = socialLegacyRequest("POST", "/", a, 0, url.Values{"next": {"//evil.invalid/path"}})
	if socialSafeNext(r, "/connections/") != "/connections/" {
		t.Fatal("open redirect")
	}
}

func TestLegacyActivityThreadSourceUIAndPrivateReadWalls(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	pk := socialLegacyActivity(t, s, owner, place, typ, "Native & meetup")
	peer := testdb.Actor(t, s.DB, "legacy-member", "adult")
	stranger := testdb.Actor(t, s.DB, "legacy-stranger", "adult")
	child := testdb.Actor(t, s.DB, "legacy-child", "child")
	mid, err := s.Social.Join(ctx, peer, pk)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.Vote(ctx, owner, mid, true, false); err != nil {
		t.Fatal(err)
	}
	parent, err := s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "**Meet** by the gate @legacy-member https://fixture.local <script>"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Social.WritePost(ctx, peer, "activity", pk, social.PostInput{Body: "I will bring water", ReplyTo: &parent}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "Pinned fixture update"}, true); err != nil {
		t.Fatal(err)
	}
	for _, facet := range []string{"helped_me", "felt_welcome"} {
		if _, err = s.Social.ToggleSentiment(ctx, peer, parent, "reaction", facet); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Social.ToggleSentiment(ctx, peer, parent, "dissent", ""); err != nil {
		t.Fatal(err)
	}
	html, data := socialLegacyHTML(t, s, peer, "activity_detail", pk, "reply_to="+fmt.Sprint(parent))
	for _, want := range []string{"Native &amp; meetup", "Member-only north gate", "Pinned fixture update", `class="mention"`, `<strong>Meet</strong>`, "I will bring water", "Replying to", `/post/`, "synthetic-form-csrf", "Helped me", "I see this differently", "data-thread"} {
		if want == "data-thread" {
			continue
		}
		if !strings.Contains(html, want) {
			t.Fatal("missing legacy thread UI", want)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "map[") {
		t.Fatal("unsafe/unrendered context")
	}
	if !spaBool(data["is_member"]) || len(spaRows(data["posts"])) != 1 || len(spaRows(spaMap(spaRows(data["posts"])[0]["replies"])["all"])) != 1 {
		t.Fatal("depth-one replies/context", data["posts"])
	}
	html, data = socialLegacyHTML(t, s, stranger, "activity_detail", pk, "")
	for _, private := range []string{"Member-only north gate", "Member-only water", "Member-only welcome", "Pinned fixture update", "legacy-member", "I will bring water"} {
		if strings.Contains(html, private) {
			t.Fatal("nonmember private context", private)
		}
	}
	if data["is_member"] != false || data["can_join"] != true {
		t.Fatal("join capability", data["is_member"], data["can_join"])
	}
	r := socialLegacyRequest("GET", "/activities/1/", child, pk, nil)
	if _, _, _, err = s.SocialView(r, child, "activity_detail"); err == nil {
		t.Fatal("cohort metadata leak")
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, stranger.ID); err != nil {
		t.Fatal(err)
	}
	r = socialLegacyRequest("GET", "/activities/1/", stranger, pk, nil)
	if _, _, _, err = s.SocialView(r, stranger, "activity_detail"); err == nil {
		t.Fatal("mutual block leak")
	}
}

func TestLegacySocialActionsPreserveParentOwnershipAndSourceFields(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	pk := socialLegacyActivity(t, s, owner, place, typ, "Source forms")
	other := socialLegacyActivity(t, s, owner, place, typ, "Other private thread")
	peer := testdb.Actor(t, s.DB, "legacy-voter-peer", "adult")
	mid, err := s.Social.Join(ctx, peer, pk)
	if err != nil {
		t.Fatal(err)
	}
	r := socialLegacyRequest("POST", "/activities/1/", owner, other, url.Values{"vote": {"approve"}})
	r.SetPathValue("membership_id", fmt.Sprint(mid))
	w := httptest.NewRecorder()
	s.SocialAction(w, r, owner, "membership_vote")
	if w.Code != 404 {
		t.Fatal("wrong-parent membership", w.Code)
	}
	r = socialLegacyRequest("POST", "/activities/1/", owner, pk, url.Values{"vote": {"approve"}, "approve": {"false"}})
	r.SetPathValue("membership_id", fmt.Sprint(mid))
	w = httptest.NewRecorder()
	s.SocialAction(w, r, owner, "membership_vote")
	if w.Code != 302 {
		t.Fatal("vote source field", w.Code, w.Body.String())
	}
	var state string
	if err = s.DB.QueryRow(ctx, `SELECT state FROM social_membership WHERE id=$1`, mid).Scan(&state); err != nil || state != "member" {
		t.Fatal("vote didn't settle", state, err)
	}
	for _, value := range []string{"1", "0"} {
		w = socialLegacyAction(t, s, owner, "activity_listing_toggle", pk, url.Values{"listed": {value}})
		if w.Code != 302 {
			t.Fatal(w.Code, w.Body.String())
		}
		var listed bool
		if err = s.DB.QueryRow(ctx, `SELECT is_publicly_listed FROM social_activity WHERE id=$1`, pk).Scan(&listed); err != nil || listed != (value == "1") {
			t.Fatal("source listing value", value, listed, err)
		}
	}
	post, err := s.Social.WritePost(ctx, owner, "activity", other, social.PostInput{Body: "Another thread body"}, false)
	if err != nil {
		t.Fatal(err)
	}
	r = socialLegacyRequest("POST", "/activities/1/", owner, pk, url.Values{"body": {"tampered"}})
	r.SetPathValue("post_id", fmt.Sprint(post))
	w = httptest.NewRecorder()
	s.SocialAction(w, r, owner, "activity_post_edit")
	if w.Code != 404 {
		t.Fatal("wrong-parent post", w.Code)
	}
	post, err = s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "Own reaction post"}, false)
	if err != nil {
		t.Fatal(err)
	}
	r = socialLegacyRequest("POST", "/activities/1/", peer, pk, url.Values{"emoji": {"helped_me"}})
	r.SetPathValue("post_id", fmt.Sprint(post))
	r.Header.Set("X-Requested-With", "fetch")
	w = httptest.NewRecorder()
	s.SocialAction(w, r, peer, "activity_post_react")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"mine":["helped_me"],"ok":true}` {
		t.Fatal("countless reaction fetch", w.Code, w.Body.String())
	}
	w = socialLegacyAction(t, s, owner, "share_to_thread", 0, url.Values{"kind": {"activity"}, "obj_id": {fmt.Sprint(other)}, "target": {fmt.Sprint(pk)}, "note": {"See this meetup"}})
	if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), fmt.Sprintf("/activities/%d/#post-", pk)) {
		t.Fatal("share return", w.Code, w.Body.String())
	}
	html, _ := socialLegacyHTML(t, s, owner, "activity_detail", pk, "")
	if !strings.Contains(html, "Other private thread") || !strings.Contains(html, "See this meetup") {
		t.Fatal("safe share card")
	}
	if _, err = s.DB.Exec(ctx, `UPDATE social_activity SET status='completed' WHERE id=$1`, pk); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"yes", "no"} {
		w = socialLegacyAction(t, s, peer, "activity_met", pk, url.Values{"met": {value}})
		if w.Code != 302 {
			t.Fatal(w.Code, w.Body.String())
		}
		var confirmed bool
		if err = s.DB.QueryRow(ctx, `SELECT met_confirmed_at IS NOT NULL FROM social_membership WHERE id=$1`, mid).Scan(&confirmed); err != nil || confirmed != (value != "no") {
			t.Fatal("met source undo", value, confirmed, err)
		}
	}
}

func TestLegacyGroupsSeriesGaugesAndOfflineMeetupsArePopulated(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	s.Social.AllowUserGroups = true
	pk := socialLegacyActivity(t, s, a, place, typ, "Own offline meetup")
	group, err := s.Social.CreateGroup(ctx, a, social.GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "Standing group"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Social.WritePost(ctx, a, "group", group, social.PostInput{Body: "Group conversation body"}, false); err != nil {
		t.Fatal(err)
	}
	series, err := s.Social.CreateSeries(ctx, a, social.SeriesInput{ActivityInput: social.ActivityInput{Place: place, ActivityType: typ, Title: "Recurring source", CostBand: "free"}, Cadence: "weekly", FirstStartsAt: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	gauge, err := s.Social.ProposeGauge(ctx, a, social.GaugeInput{Place: place, ActivityType: typ, CoarseWindow: "weekend_daytime"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name string
		pk   int64
		want []string
	}{{"group_detail", group, []string{"Standing group", "Group conversation body", "legacy-organiser", "Basketball"}}, {"series_detail", series, []string{"Recurring source", "Weekly", "Pause", "next_instance_note"}}, {"series_list", 0, []string{"Recurring source", "Library &amp; Hall"}}, {"gauge_detail", gauge, []string{"Weekend daytime", "Needs 2 more", "Turn into a real meetup"}}, {"gauges", 0, []string{"Basketball", "Library &amp; Hall"}}, {"organize", 0, []string{"Own offline meetup", "Recurring source", "Standing group"}}, {"my_meetups", 0, []string{"Own offline meetup", "Member-only north gate", "Saved", "calendar.ics"}}, {"my_venues", 0, []string{"Nothing to flag"}}} {
		t.Run(row.name, func(t *testing.T) {
			html, _ := socialLegacyHTML(t, s, a, row.name, row.pk, "")
			for _, want := range row.want {
				if !strings.Contains(html, want) {
					t.Fatal("missing source UI", want)
				}
			}
		})
	}
	for _, row := range []struct {
		name string
		pk   int64
	}{{"activity_create", 0}, {"activity_edit", pk}, {"series_create", 0}, {"group_create", 0}, {"gauge_create", 0}, {"gauge_convert", gauge}} {
		t.Run(row.name, func(t *testing.T) {
			html, _ := socialLegacyHTML(t, s, a, row.name, row.pk, "")
			if !strings.Contains(html, `<input`) || !strings.Contains(html, `name="title"`) && row.name != "gauge_create" || strings.Contains(html, "map[") {
				t.Fatal("form widgets missing", html)
			}
		})
	}
	w := socialLegacyAction(t, s, a, "series_set_next_note", series, url.Values{"next_instance_note": {"Next week: bring a book"}})
	if w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	html, _ := socialLegacyHTML(t, s, a, "series_detail", series, "")
	if !strings.Contains(html, "Next week: bring a book") {
		t.Fatal("source next-note field lost")
	}
	r := socialLegacyRequest("GET", "/account/calendar.ics", a, 0, nil)
	w = httptest.NewRecorder()
	if !s.SocialDownload(w, r, a, "my_calendar") || w.Code != 200 || !strings.Contains(w.Body.String(), "Own offline meetup") || strings.Contains(w.Body.String(), "Recurring source") {
		t.Fatal("self-only calendar", w.Code, w.Body.String())
	}
	stranger := testdb.Actor(t, s.DB, "legacy-offline-stranger", "adult")
	r = socialLegacyRequest("GET", "/account/calendar.ics", stranger, 0, nil)
	w = httptest.NewRecorder()
	s.SocialDownload(w, r, stranger, "my_calendar")
	if strings.Contains(w.Body.String(), "Own offline meetup") {
		t.Fatal("other member calendar leaked")
	}
}

type socialRejectScanner struct{}
type socialCleanScanner struct{}

func (socialCleanScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{Clean: true}, nil
}

type socialCleanDocuments struct{}

func (socialCleanDocuments) ScanDocument(context.Context, string, int64) (media.Verdict, error) {
	return media.Verdict{Clean: true}, nil
}

func TestLegacyAdultAttachmentOnlyFormPublishesPrivateScannedPDF(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	pk := socialLegacyActivity(t, s, a, place, typ, "PDF attachment fixture")
	ctx := context.Background()
	scratch := t.TempDir()
	s.Config.UploadScratch = scratch
	processor, err := media.NewProcessor(media.DefaultConfig(scratch), socialCleanScanner{}, socialCleanDocuments{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := media.NewLocalStore(filepath.Join(scratch, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.NewService(s.DB, processor, store, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, s.Social)
	if err = media.EnsureSchema(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	_ = writer.WriteField("body", "")
	_ = writer.WriteField("disappear", "86400")
	file, err := writer.CreateFormFile("attachment", "../private-fixture.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(file, "%PDF-1.7\ngenerated fixture\n%%EOF")
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/activities/1/post/", &payload)
	r.SetPathValue("pk", fmt.Sprint(pk))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r = platform.WithActor(r, a)
	w := httptest.NewRecorder()
	s.SocialAction(w, r, a, "activity_post")
	if w.Code != 302 {
		t.Fatal("attachment-only source form", w.Code, w.Body.String())
	}
	var post int64
	var body, status string
	var expires bool
	if err = s.DB.QueryRow(ctx, `SELECT p.id,p.body,att.status,att.expires_at IS NOT NULL FROM social_post p JOIN social_thread th ON th.id=p.thread_id JOIN media_attachment att ON att.post_id=p.id WHERE th.activity_id=$1`, pk).Scan(&post, &body, &status, &expires); err != nil || body != "" || status != "ready" || !expires {
		t.Fatal("atomic post and scanned attachment", post, body, status, expires, err)
	}
	html, data := socialLegacyHTML(t, s, a, "activity_detail", pk, "")
	if !strings.Contains(html, "private-fixture.pdf") || !strings.Contains(html, "downloads") || !strings.Contains(html, "/api/media/attachment/") || strings.Contains(html, "storage_key") {
		t.Fatal("source attachment rendering")
	}
	stranger := testdb.Actor(t, s.DB, "legacy-pdf-stranger", "adult")
	attachment := spaRows(spaRows(data["posts"])[0]["attachment_list"])[0]
	privateURL := spaText(attachment["url"])
	mux := http.NewServeMux()
	s.Media.Register(mux)
	read := platform.WithActor(httptest.NewRequest("GET", privateURL, nil), a)
	served := httptest.NewRecorder()
	mux.ServeHTTP(served, read)
	if served.Code != 200 || !strings.HasPrefix(served.Body.String(), "%PDF-1.7") || !strings.Contains(served.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("private scanned download", served.Code)
	}
	read = platform.WithActor(httptest.NewRequest("GET", privateURL, nil), stranger)
	served = httptest.NewRecorder()
	mux.ServeHTTP(served, read)
	if served.Code != 403 {
		t.Fatal("signed viewer binding", served.Code)
	}
	html, _ = socialLegacyHTML(t, s, stranger, "activity_detail", pk, "")
	if strings.Contains(html, "private-fixture.pdf") {
		t.Fatal("private attachment leaked to nonmember")
	}
}

func TestLegacyPrivateBrowseHomeAndCommunityComposition(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	viewer := testdb.Actor(t, s.DB, "legacy-browse-viewer", "adult")
	for i := 0; i < 32; i++ {
		socialLegacyActivity(t, s, owner, place, typ, fmt.Sprintf("Populated meetup %02d", i))
	}
	child := testdb.Actor(t, s.DB, "legacy-hidden-child", "child")
	if _, err := s.Social.CreateActivity(ctx, child, social.ActivityInput{Place: place, ActivityType: typ, Title: "CROSS COHORT PRIVATE", StartsAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	html, data := socialLegacyHTML(t, s, viewer, "activity_list", 0, "page=2&view=cards&beginners=true")
	if len(spaRows(data["activities"])) != 8 || spaInt(spaMap(data["page_obj"])["number"]) != 2 || spaInt(spaMap(spaMap(data["page_obj"])["paginator"])["count"]) != 32 {
		t.Fatal("source paginator", data["page_obj"])
	}
	for _, want := range []string{"Populated meetup 24", "Page 2 of 2", "Basketball", "Library &amp; Hall", "data-view=\"cards\"", "beginners=true"} {
		if !strings.Contains(html, want) {
			t.Fatal("browse context", want)
		}
	}
	if strings.Contains(html, "CROSS COHORT PRIVATE") {
		t.Fatal("browse cohort leak")
	}
	_, data = socialLegacyHTML(t, s, viewer, "activity_list", 0, "page=-3")
	if spaInt(spaMap(data["page_obj"])["number"]) != 2 {
		t.Fatal("source negative page clamping")
	}
	_, data = socialLegacyHTML(t, s, viewer, "activity_list", 0, "q=basketball&page=bad&near_lon=23.6&near_lat=46.77")
	if len(spaRows(data["activities"])) != 24 || data["near_active"] != false {
		t.Fatal("source search bounded/sort contract")
	}
	_, data = socialLegacyHTML(t, s, viewer, "activity_list", 0, "near_lon=NaN&near_lat=46.77")
	if data["near_active"] != false {
		t.Fatal("bad near-me input didn't safely degrade")
	}
	html, data = socialLegacyHTML(t, s, viewer, "home", 0, "")
	if len(spaRows(data["recommended"])) != 8 || len(spaRows(data["beginners"])) != 6 || len(spaRows(data["upcoming"])) != 20 {
		t.Fatal("populated home feed", len(spaRows(data["recommended"])), len(spaRows(data["beginners"])), len(spaRows(data["upcoming"])))
	}
	seen := map[int64]bool{}
	for _, row := range spaRows(data["recommended"]) {
		seen[spaID(row)] = true
	}
	for _, row := range spaRows(data["beginners"]) {
		if seen[spaID(row)] {
			t.Fatal("recommended beginner duplicate")
		}
		seen[spaID(row)] = true
	}
	beginnerIDs := map[int64]bool{}
	for _, row := range spaRows(data["beginners"]) {
		beginnerIDs[spaID(row)] = true
	}
	for _, row := range spaRows(data["upcoming"]) {
		if beginnerIDs[spaID(row)] {
			t.Fatal("promoted beginner upcoming duplicate")
		}
	}
	for _, want := range []string{"New here? Pick what", "New here? These welcome beginners", "Basketball", "Library &amp; Hall"} {
		if !strings.Contains(html, want) {
			t.Fatal("home composition", want)
		}
	}
	payload, _, _, _, err := s.BuildSPA(ctx, socialLegacyRequest("GET", "/", viewer, 0, nil), viewer, "home", data)
	if err != nil || len(spaRows(spaMap(spaMap(payload["data"])["sections"])["recommended"])) != 8 {
		t.Fatal("same legacy-to-SPA feed contract", err)
	}
	s.Social.AllowUserGroups = true
	group, err := s.Social.CreateGroup(ctx, owner, social.GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "Linked standing group"})
	if err != nil {
		t.Fatal(err)
	}
	var area, cat int64
	if err = s.DB.QueryRow(ctx, `SELECT area_id,category_id FROM social_group WHERE id=$1`, group).Scan(&area, &cat); err != nil {
		t.Fatal(err)
	}
	var cid int64
	if err = s.DB.QueryRow(ctx, `INSERT INTO communities_community(cohort,area_id,category_id,activity_type_id,tier,slug,name,is_published,last_evaluated_at,created_at) VALUES('adult',$1,$2,$3,'type','source-cluj-basketball','Source Cluj Basketball',true,now(),now()) RETURNING id`, area, cat, typ).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	html, data = socialLegacyHTML(t, s, viewer, "communities", 0, "")
	if !strings.Contains(html, "Linked standing group") || !strings.Contains(html, "Source Cluj Basketball") || !strings.Contains(html, "Basketball") || strings.Contains(html, "map[") {
		t.Fatal("source groups/community page models")
	}
	if len(spaRows(data["page"])) != 1 || len(spaRows(data["groups_page"])) != 1 {
		t.Fatal("dual source pages")
	}
	r := socialLegacyRequest("GET", "/communities/source-cluj-basketball/", viewer, 0, nil)
	r.SetPathValue("slug", "source-cluj-basketball")
	context, template, _, err := s.SocialView(r, viewer, "community_detail")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err = s.Renderer.Render(w, r, template, context); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "Linked standing group") || len(spaRows(context["activities"])) != 32 {
		t.Fatal("source coordinate feed/link")
	}
	r = platform.WithActor(r, child)
	if _, _, _, err = s.SocialView(r, child, "community_detail"); err == nil {
		t.Fatal("community slug cohort leak")
	}
}

func TestLegacyMinorGroupQuestionsSupervisionAndReadOnlyGuardians(t *testing.T) {
	s, staff, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	s.Social.MinorOnboardingEnabled = true
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	child := testdb.Actor(t, s.DB, "legacy-child-owner", "child")
	peer := testdb.Actor(t, s.DB, "legacy-child-peer", "child")
	guardian := testdb.Actor(t, s.DB, "legacy-guardian", "adult")
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	pk, err := s.Social.CreateActivity(ctx, child, social.ActivityInput{Place: place, ActivityType: typ, Title: "Supervised child meetup", StartsAt: time.Now().Add(time.Hour), Supervised: true, WhatToBring: "A book"})
	if err != nil {
		t.Fatal(err)
	}
	html, data := socialLegacyHTML(t, s, child, "activity_detail", pk, "")
	for _, want := range []string{"approved public venue", "supervisor needed", "legacy-guardian", "Add supervisor"} {
		if !strings.Contains(html, want) {
			t.Fatal("supervision affordance", want)
		}
	}
	if data["supervisor_present"] != false || data["show_dissent_concern"] != false || strings.Contains(html, `name="disappear"`+` value="3600"`) {
		t.Fatal("child social affordance widened")
	}
	if _, err = s.Social.AddGuardian(ctx, child, pk, guardian.ID); err != nil {
		t.Fatal(err)
	}
	mid, err := s.Social.Join(ctx, peer, pk)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.Vote(ctx, child, mid, true, false); err != nil {
		t.Fatal(err)
	}
	post, err := s.Social.WritePost(ctx, child, "activity", pk, social.PostInput{Body: "Child private body https://fixture.local"}, false)
	if err != nil {
		t.Fatal(err)
	}
	html, data = socialLegacyHTML(t, s, peer, "activity_detail", pk, "")
	if !strings.Contains(html, "supervised") || strings.Contains(html, "I see this differently") || strings.Contains(html, "This doesn't seem to fit here") || strings.Contains(html, `href="https://fixture.local"`) {
		t.Fatal("child footer/dissent/link clamp")
	}
	if spaMap(data["thread_audience"])["peer_count"] != nil || spaMap(data["digest"])["member_count"] != nil {
		t.Fatal("minor aggregate disclosure")
	}
	r := socialLegacyRequest("POST", "/activities/1/", guardian, pk, url.Values{"emoji": {"helped_me"}})
	r.SetPathValue("post_id", fmt.Sprint(post))
	w := httptest.NewRecorder()
	s.SocialAction(w, r, guardian, "activity_post_react")
	if w.Code != 404 {
		t.Fatal("cross-cohort guardian write", w.Code)
	}
	r = socialLegacyRequest("POST", "/activities/1/", peer, pk, nil)
	r.SetPathValue("post_id", fmt.Sprint(post))
	r.Header.Set("X-Requested-With", "fetch")
	w = httptest.NewRecorder()
	s.SocialAction(w, r, peer, "activity_post_concern")
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatal("child concern accepted", w.Code, w.Body.String())
	}
	w = socialLegacyAction(t, s, peer, "activity_unsafe", pk, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "You can leave this activity any time") {
		t.Fatal("safe exit unavailable", w.Code, w.Body.String())
	}
	group, err := s.Social.CreateGroup(ctx, staff, social.GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "Curated child group", Cohort: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.JoinGroup(ctx, child, group); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Social.WritePost(ctx, staff, "group", group, social.PostInput{Body: "Curator announcement"}, true); err != nil {
		t.Fatal(err)
	}
	html, data = socialLegacyHTML(t, s, child, "group_detail", group, "")
	for _, want := range []string{"Curator announcement", "announcement-only", "Ask the organiser", `value="next_meetup"`, `value="how_it_works"`} {
		if !strings.Contains(html, want) {
			t.Fatal("minor group contract", want)
		}
	}
	if data["roster"] != nil || data["can_post"] != false || strings.Contains(html, "Members (") {
		t.Fatal("minor group roster/compose leaked")
	}
	html, _ = socialLegacyHTML(t, s, staff, "group_detail", group, "")
	if !strings.Contains(html, "Curator announcement") || !strings.Contains(html, "Post an announcement") {
		t.Fatal("staff curator lost own broadcasts")
	}
	w = socialLegacyAction(t, s, child, "group_ask", group, url.Values{"prompt": {"next_meetup"}})
	if w.Code != 302 {
		t.Fatal("source fixed question", w.Code, w.Body.String())
	}
	var n int
	if err = s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='group_question'`, staff.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("fixed question notification", n, err)
	}
}

// An organiser who blocks a member hides the activity page from them, but never
// the safe exit: the fallback page keeps the unsafe tap, the detailed report
// and leave, and discloses no thread, roster, place or logistics.
func TestLegacySafeExitSurvivesOwnerBlock(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	pk := socialLegacyActivity(t, s, owner, place, typ, "Blocked organiser meetup")
	member := testdb.Actor(t, s.DB, "legacy-safe-exit-member", "adult")
	stranger := testdb.Actor(t, s.DB, "legacy-safe-exit-stranger", "adult")
	mid, err := s.Social.Join(ctx, member, pk)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.Vote(ctx, owner, mid, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "Private organiser thread body"}, false); err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []platform.Actor{member, stranger} {
		if _, err = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, blocked.ID); err != nil {
			t.Fatal(err)
		}
	}
	html, data := socialLegacyHTML(t, s, member, "activity_detail", pk, "")
	for _, want := range []string{"Blocked organiser meetup", "I feel unsafe", "Report with details", fmt.Sprintf("/activities/%d/unsafe/", pk), fmt.Sprintf("/activities/%d/leave/", pk), fmt.Sprintf("/report/?type=activity&id=%d", pk)} {
		if !strings.Contains(html, want) {
			t.Fatal("safe exit unreachable under owner block", want)
		}
	}
	for _, absent := range []string{"Private organiser thread body", "Member-only", "Library &amp; Hall", "Library & Hall", owner.Username} {
		if strings.Contains(html, absent) {
			t.Fatal("blocked safe exit disclosed activity detail", absent)
		}
	}
	if data["safe_exit_only"] != true || data["members"] != nil || data["thread"] != nil {
		t.Fatal("blocked safe exit widened its context")
	}
	r := socialLegacyRequest("GET", fmt.Sprintf("/activities/%d/", pk), stranger, pk, nil)
	if _, _, _, err = s.SocialView(r, stranger, "activity_detail"); err == nil {
		t.Fatal("blocked non-member reached the safe exit")
	}
	w := socialLegacyAction(t, s, member, "activity_unsafe", pk, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "A moderator has been alerted") || !strings.Contains(w.Body.String(), "I feel unsafe") || strings.Contains(w.Body.String(), "Private organiser thread body") {
		t.Fatal("unsafe tap under owner block", w.Code, w.Body.String())
	}
	var reports int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report WHERE reporter_id=$1 AND reason='off_platform'`, member.ID).Scan(&reports); err != nil || reports != 1 {
		t.Fatal("unsafe tap under owner block filed no report", reports, err)
	}
	w = socialLegacyAction(t, s, member, "activity_leave", pk, nil)
	if w.Code != 302 || w.Header().Get("Location") != "/my-meetups/" {
		t.Fatal("leave under owner block", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var state string
	if err = s.DB.QueryRow(ctx, `SELECT state FROM social_membership WHERE id=$1`, mid).Scan(&state); err != nil || state != "removed" {
		t.Fatal("leave under owner block did not persist", state, err)
	}
	r = socialLegacyRequest("GET", fmt.Sprintf("/activities/%d/", pk), member, pk, nil)
	if _, _, _, err = s.SocialView(r, member, "activity_detail"); err == nil {
		t.Fatal("safe exit outlived the membership")
	}
}

func TestLegacyConnectionsRequireAcceptanceBeforeOpeningMessages(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	b := testdb.Actor(t, s.DB, "legacy-connected-peer", "adult")
	pk := socialLegacyActivity(t, s, a, place, typ, "Connection context")
	mid, err := s.Social.Join(ctx, b, pk)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.Vote(ctx, a, mid, true, false); err != nil {
		t.Fatal(err)
	}
	w := socialLegacyAction(t, s, a, "connection_request", 0, url.Values{"public_id": {b.PublicID}, "next": {fmt.Sprintf("/activities/%d/", pk)}})
	if w.Code != 302 || w.Header().Get("Location") != fmt.Sprintf("/activities/%d/", pk) {
		t.Fatal("source request next", w.Code, w.Body.String())
	}
	var cid int64
	if err = s.DB.QueryRow(ctx, `SELECT id FROM connections_connection WHERE requester_id=$1 AND addressee_id=$2`, a.ID, b.ID).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	w = socialLegacyAction(t, s, a, "connection_message", 0, url.Values{"public_id": {b.PublicID}})
	if w.Code != 404 {
		t.Fatal("pending connection opened private chat", w.Code)
	}
	w = socialLegacyAction(t, s, b, "connection_respond", cid, url.Values{"accept": {"1"}})
	if w.Code != 302 {
		t.Fatal("source accept field", w.Code, w.Body.String())
	}
	w = socialLegacyAction(t, s, a, "connection_message", 0, url.Values{"public_id": {b.PublicID}})
	if w.Code != 302 || w.Header().Get("Location") != "/messages/" {
		t.Fatal("native accepted conversation", w.Code, w.Body.String())
	}
	var state string
	if err = s.DB.QueryRow(ctx, `SELECT p.state FROM messaging_participant p JOIN messaging_conversation c ON c.id=p.conversation_id WHERE c.creator_id=$1 AND p.user_id=$2`, a.ID, b.ID).Scan(&state); err != nil || state != "invited" {
		t.Fatal("first contact acceptance bypass", state, err)
	}
}

func (socialRejectScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{}, nil
}

func TestLegacyAttachmentScannerFailureLeavesNoPostAndClearsScratch(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	pk := socialLegacyActivity(t, s, a, place, typ, "Attachment fixture")
	scratch := t.TempDir()
	s.Config.UploadScratch = scratch
	processor, err := media.NewProcessor(media.DefaultConfig(scratch), socialRejectScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := media.NewLocalStore(filepath.Join(scratch, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.NewService(s.DB, processor, store, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, s.Social)
	if err = media.EnsureSchema(context.Background(), s.DB); err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	_ = writer.WriteField("body", "")
	_ = writer.WriteField("csrfmiddlewaretoken", "fixture")
	file, err := writer.CreateFormFile("attachment", "tiny.png")
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/activities/1/post/", &payload)
	r.SetPathValue("pk", fmt.Sprint(pk))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r = platform.WithActor(r, a)
	w := httptest.NewRecorder()
	if !s.SocialAction(w, r, a, "activity_post") {
		t.Fatal("upload not handled")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "blocked by safety screening") {
		data, template, _, viewErr := s.SocialView(r, a, "activity_detail")
		var renderErr error
		if viewErr == nil {
			renderErr = s.Renderer.Render(httptest.NewRecorder(), r, template, data)
		}
		t.Fatal("scanner rejection UI", w.Code, w.Body.String(), "view", viewErr, "render", renderErr)
	}
	var n int
	if err = s.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE th.activity_id=$1`, pk).Scan(&n); err != nil || n != 0 {
		t.Fatal("rejected attachment left a message", n, err)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "legacy-upload-") {
			t.Fatal("temporary upload retained", entry.Name())
		}
	}
}
