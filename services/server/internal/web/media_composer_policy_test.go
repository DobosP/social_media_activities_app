package web

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func mediaComposerFragment(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `<form method="post" id="compose"`)
	if start < 0 {
		t.Fatal("member composer is missing")
	}
	end := strings.Index(page[start:], "</form>")
	if end < 0 {
		t.Fatal("member composer is incomplete")
	}
	return page[start : start+end+len("</form>")]
}

func mediaComposerPlain(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

type mediaComposerDocumentScanner struct {
	clean bool
	calls int
}

func (s *mediaComposerDocumentScanner) ScanDocument(context.Context, string, int64) (media.Verdict, error) {
	s.calls++
	return media.Verdict{Clean: s.clean}, nil
}

func mediaComposerPDFRequest(t *testing.T, actor platform.Actor, pk int64, ttl string) *http.Request {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for name, value := range map[string]string{"body": "", "disappear": ttl} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("attachment", "../configured-private.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(file, "%PDF-1.7\nsynthetic configured TTL fixture\n%%EOF"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/activities/1/post/", &payload)
	r.SetPathValue("pk", fmt.Sprint(pk))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return platform.WithActor(r, actor)
}

func TestPostgresMediaComposerUsesEffectivePolicyCapabilitiesAndTTL(t *testing.T) {
	s, adult, place, typ := socialLegacyFixture(t)
	actors := map[string]platform.Actor{"adult": adult,
		"teen":  testdb.Actor(t, s.DB, "composer-policy-teen", "teen"),
		"child": testdb.Actor(t, s.DB, "composer-policy-child", "child")}
	activities := map[string]int64{}
	for _, cohort := range []string{"adult", "teen", "child"} {
		activities[cohort] = socialLegacyActivity(t, s, actors[cohort], place, typ, cohort+" policy composer")
	}
	defaults := [][2]string{{"3600", "1 hour"}, {"86400", "1 day"}, {"604800", "1 week"}}
	minorDefaults := defaults[1:]
	cases := []struct {
		name, cohort           string
		configure              func(*media.PolicyConfig)
		nilMedia, disableVideo bool
		images, files, videos  bool
		options                [][2]string
	}{
		{name: "adult defaults", cohort: "adult", images: true, files: true, videos: true, options: defaults},
		{name: "adult PDFs disabled", cohort: "adult", configure: func(c *media.PolicyConfig) { c.FileCohorts = map[string]bool{} }, images: true, videos: true, options: defaults},
		{name: "adult videos disabled by cohort", cohort: "adult", configure: func(c *media.PolicyConfig) { c.VideoCohorts = map[string]bool{} }, images: true, files: true, options: defaults},
		{name: "adult videos disabled by processor", cohort: "adult", disableVideo: true, images: true, files: true, options: defaults},
		{name: "adult images only", cohort: "adult", configure: func(c *media.PolicyConfig) { c.FileCohorts, c.VideoCohorts = map[string]bool{}, map[string]bool{} }, images: true, options: defaults},
		{name: "attachment kill switch", cohort: "adult", configure: func(c *media.PolicyConfig) { c.AttachmentsEnabled = false }},
		{name: "media unavailable", cohort: "adult", nilMedia: true},
		{name: "teen defaults", cohort: "teen", images: true, options: minorDefaults},
		{name: "child defaults", cohort: "child", images: true, options: minorDefaults},
		{name: "adult two hour floor", cohort: "adult", configure: func(c *media.PolicyConfig) { c.EphemeralMinTTL = 2 * time.Hour }, images: true, files: true, videos: true, options: [][2]string{{"7200", "2 hours"}, {"86400", "1 day"}, {"604800", "1 week"}}},
		{name: "adult deduplicated day floor", cohort: "adult", configure: func(c *media.PolicyConfig) { c.EphemeralMinTTL = 24 * time.Hour }, images: true, files: true, videos: true, options: minorDefaults},
		{name: "teen two day floor", cohort: "teen", configure: func(c *media.PolicyConfig) { c.EphemeralMinTTLMinors = 48 * time.Hour }, images: true, options: [][2]string{{"172800", "2 days"}, {"604800", "1 week"}}},
		{name: "child two day floor", cohort: "child", configure: func(c *media.PolicyConfig) { c.EphemeralMinTTLMinors = 48 * time.Hour }, images: true, options: [][2]string{{"172800", "2 days"}, {"604800", "1 week"}}},
		{name: "child deduplicated week floor", cohort: "child", configure: func(c *media.PolicyConfig) { c.EphemeralMinTTLMinors = 7 * 24 * time.Hour }, images: true, options: [][2]string{{"604800", "1 week"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.nilMedia {
				s.Media = nil
			} else {
				cfg := media.DefaultConfig(t.TempDir())
				cfg.VideoEnabled = !tc.disableVideo
				processor, err := media.NewProcessor(cfg, socialCleanScanner{}, socialCleanDocuments{})
				if err != nil {
					t.Fatal(err)
				}
				s.Media = media.NewService(s.DB, processor, nil, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, s.Social)
				policy := media.DefaultPolicyConfig()
				if tc.configure != nil {
					tc.configure(&policy)
				}
				if err = s.Media.ConfigurePolicy(policy); err != nil {
					t.Fatal(err)
				}
			}
			page, data := socialLegacyHTML(t, s, actors[tc.cohort], "activity_detail", activities[tc.cohort], "")
			composer := mediaComposerFragment(t, page)
			if data["media_capabilities_supplied"] != true {
				t.Fatal("native composer omitted its explicit policy capability marker")
			}
			if !strings.Contains(composer, `name="body"`) || !regexp.MustCompile(`(?s)<button[^>]*type="submit"[^>]*>\s*Post\s*</button>`).MatchString(composer) {
				t.Fatal("media policy removed text posting")
			}
			if spaBool(data["attachments_enabled"]) != tc.images || spaBool(data["file_enabled"]) != tc.files || spaBool(data["video_enabled"]) != tc.videos {
				t.Fatal("composer context does not reflect effective policy", data["attachments_enabled"], data["file_enabled"], data["video_enabled"])
			}
			input := regexp.MustCompile(`<input[^>]*name="attachment"[^>]*>`).FindString(composer)
			if !tc.images {
				if input != "" || strings.Contains(composer, `name="disappear"`) || strings.Contains(composer, "compose-upload-note") {
					t.Fatal("unavailable attachment controls or copy are visible")
				}
				return
			}
			if input == "" {
				t.Fatal("available attachment input is missing")
			}
			accept := regexp.MustCompile(`accept="([^"]*)"`).FindStringSubmatch(input)
			if len(accept) != 2 || html.UnescapeString(accept[1]) != spaText(data["attachment_accept"]) {
				t.Fatal("MIME control does not consume effective policy")
			}
			wantMIME := []string{"image/png", "image/jpeg", "image/webp"}
			if tc.files {
				wantMIME = append(wantMIME, "application/pdf")
			}
			if tc.videos {
				wantMIME = append(wantMIME, "video/mp4", "video/quicktime", "video/webm")
			}
			gotMIME := strings.Split(accept[1], ",")
			sort.Strings(gotMIME)
			sort.Strings(wantMIME)
			if !reflect.DeepEqual(gotMIME, wantMIME) {
				t.Fatal("wrong accepted media for viewer policy", gotMIME, wantMIME)
			}
			id := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(input)
			if len(id) != 2 {
				t.Fatal("attachment input lacks accessible identity")
			}
			label := regexp.MustCompile(`(?s)<label[^>]*for="` + regexp.QuoteMeta(id[1]) + `"[^>]*>(.*?)</label>`).FindStringSubmatch(composer)
			if len(label) != 2 || !strings.Contains(mediaComposerPlain(label[1]), "Attach a photo") || strings.Contains(label[1], "PDF") != tc.files || strings.Contains(label[1], "short video") != tc.videos {
				t.Fatal("attachment accessible label promises unavailable formats", label)
			}
			help := regexp.MustCompile(`(?s)<p class="muted compose-upload-note">(.*?)</p>`).FindStringSubmatch(composer)
			if len(help) != 2 {
				t.Fatal("attachment privacy/screening guidance missing")
			}
			plainHelp := mediaComposerPlain(help[1])
			if !strings.Contains(plainHelp, "private to members") || !strings.Contains(plainHelp, "safety-screened on upload") || strings.Contains(plainHelp, "PDF") != tc.files || strings.Contains(plainHelp, "short videos") != tc.videos {
				t.Fatal("attachment guidance does not reflect private screened formats", plainHelp)
			}
			options := regexp.MustCompile(`<option value="([^"]*)">([^<]*)</option>`).FindAllStringSubmatch(composer, -1)
			gotOptions := [][2]string{}
			for _, option := range options {
				if option[1] != "" {
					gotOptions = append(gotOptions, [2]string{option[1], mediaComposerPlain(option[2])})
				}
			}
			wantOptions := [][2]string{}
			for _, option := range tc.options {
				wantOptions = append(wantOptions, [2]string{option[0], "Disappear after " + option[1]})
			}
			if !reflect.DeepEqual(gotOptions, wantOptions) {
				t.Fatal("disappearance option value/label is stale or duplicated", gotOptions, wantOptions)
			}
		})
	}
}

func TestPostgresMediaComposerLegacyContextPreservesReferenceControls(t *testing.T) {
	s, actor, place, typ := socialLegacyFixture(t)
	pk := socialLegacyActivity(t, s, actor, place, typ, "Reference composer compatibility")
	policy := media.DefaultPolicyConfig()
	policy.AttachmentsEnabled = false
	if err := s.Media.ConfigurePolicy(policy); err != nil {
		t.Fatal(err)
	}
	r := socialLegacyRequest("GET", "/activities/"+fmt.Sprint(pk)+"/", actor, pk, nil)
	data, template, handled, err := s.SocialView(r, actor, "activity_detail")
	if !handled || err != nil || data["media_capabilities_supplied"] != true {
		t.Fatal("native reference fixture does not carry explicit policy capabilities", handled, err)
	}
	if spaBool(data["attachments_enabled"]) || spaBool(data["file_enabled"]) || spaText(data["attachment_accept"]) != "" {
		t.Fatal("disabled native policy fixture is not restrictive")
	}
	// The offline Python reference does not supply the native marker or native
	// capability fields. Stale native false/empty fields must also be ignored when
	// that marker is absent, while a supplied marker always keeps them authoritative.
	delete(data, "media_capabilities_supplied")
	data["csrf"] = "synthetic-reference-form-csrf"
	data["ephemeral_options"] = []any{[]string{"3600", "1 hour"}, []string{"86400", "1 day"}, []string{"604800", "1 week"}}
	for _, videoEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("video=%t", videoEnabled), func(t *testing.T) {
			data["video_enabled"] = videoEnabled
			w := httptest.NewRecorder()
			if err := s.Renderer.Render(w, r, template, data); err != nil {
				t.Fatal(err)
			}
			composer := mediaComposerFragment(t, w.Body.String())
			input := regexp.MustCompile(`<input[^>]*name="attachment"[^>]*>`).FindString(composer)
			accept := regexp.MustCompile(`accept="([^"]*)"`).FindStringSubmatch(input)
			wantAccept := "image/png,image/jpeg,image/webp,application/pdf"
			wantLabel := "Attach a photo or PDF"
			if videoEnabled {
				wantAccept += ",video/mp4,video/quicktime,video/webm"
				wantLabel = "Attach a photo, PDF, or short video"
			}
			if len(accept) != 2 || accept[1] != wantAccept {
				t.Fatal("legacy context lost its original image/PDF/video MIME controls", accept, wantAccept)
			}
			id := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(input)
			if len(id) != 2 {
				t.Fatal("legacy attachment input lost accessible identity")
			}
			label := regexp.MustCompile(`(?s)<label[^>]*for="` + regexp.QuoteMeta(id[1]) + `"[^>]*>(.*?)</label>`).FindStringSubmatch(composer)
			if len(label) != 2 || mediaComposerPlain(label[1]) != wantLabel {
				t.Fatal("legacy context lost its original accessible attachment label", label, wantLabel)
			}
			help := regexp.MustCompile(`(?s)<p class="muted compose-upload-note">(.*?)</p>`).FindStringSubmatch(composer)
			if len(help) != 2 {
				t.Fatal("legacy context lost its original upload guidance")
			}
			plainHelp := mediaComposerPlain(help[1])
			if !strings.Contains(plainHelp, "Photos") || !strings.Contains(plainHelp, "PDFs") || !strings.Contains(plainHelp, "private to members") || !strings.Contains(plainHelp, "safety-screened on upload") || strings.Contains(plainHelp, "short videos") != videoEnabled {
				t.Fatal("legacy context lost its original media/privacy/screening guidance", plainHelp)
			}
			gotOptions := [][2]string{}
			for _, option := range regexp.MustCompile(`<option value="([^"]*)">([^<]*)</option>`).FindAllStringSubmatch(composer, -1) {
				gotOptions = append(gotOptions, [2]string{option[1], mediaComposerPlain(option[2])})
			}
			wantOptions := [][2]string{{"", "Keep"}, {"3600", "Disappear after 1 hour"}, {"86400", "Disappear after 1 day"}, {"604800", "Disappear after 1 week"}}
			if !reflect.DeepEqual(gotOptions, wantOptions) {
				t.Fatal("legacy context lost its original keep/disappearance options", gotOptions)
			}
		})
	}
}

func TestPostgresMediaComposerCustomTTLPublishesPrivateScannedPDF(t *testing.T) {
	s, actor, place, typ := socialLegacyFixture(t)
	pk := socialLegacyActivity(t, s, actor, place, typ, "Custom disappearance PDF")
	ctx := context.Background()
	scratch := t.TempDir()
	s.Config.UploadScratch = scratch
	documents := &mediaComposerDocumentScanner{clean: true}
	processor, err := media.NewProcessor(media.DefaultConfig(scratch), socialCleanScanner{}, documents)
	if err != nil {
		t.Fatal(err)
	}
	store, err := media.NewLocalStore(filepath.Join(scratch, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.NewService(s.DB, processor, store, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, s.Social)
	policy := media.DefaultPolicyConfig()
	policy.EphemeralMinTTL = 2 * time.Hour
	if err = s.Media.ConfigurePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if err = media.EnsureSchema(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	for _, submittedTTL := range []string{"7200", "3600", "0", ""} {
		t.Run(submittedTTL, func(t *testing.T) {
			r := mediaComposerPDFRequest(t, actor, pk, submittedTTL)
			w := httptest.NewRecorder()
			started := time.Now()
			priorScans := documents.calls
			if !s.SocialAction(w, r, actor, "activity_post") || w.Code != 302 {
				t.Fatal("effective or legacy TTL option refused", w.Code)
			}
			if documents.calls != priorScans+1 {
				t.Fatal("configured TTL bypassed document screening", documents.calls, priorScans)
			}
			var post int64
			var body, status, kind string
			var expires *time.Time
			if err = s.DB.QueryRow(ctx, `SELECT p.id,p.body,att.status,att.kind,att.expires_at FROM social_post p JOIN social_thread th ON th.id=p.thread_id JOIN media_attachment att ON att.post_id=p.id WHERE th.activity_id=$1 ORDER BY p.id DESC LIMIT 1`, pk).Scan(&post, &body, &status, &kind, &expires); err != nil || body != "" || status != "ready" || kind != "file" {
				t.Fatal("scanned post/attachment or effective two-hour expiry missing", post, body, status, kind, expires, err)
			}
			keep := submittedTTL == "0" || submittedTTL == ""
			if keep && expires != nil || !keep && (expires == nil || expires.Before(started.Add(2*time.Hour)) || expires.After(time.Now().Add(2*time.Hour))) {
				t.Fatal("configured TTL changed keep or effective two-hour expiry", submittedTTL, expires)
			}
			page, data := socialLegacyHTML(t, s, actor, "activity_detail", pk, "")
			var privateURL string
			for _, p := range spaRows(data["posts"]) {
				if spaID(p) == post {
					attachments := spaRows(p["attachment_list"])
					if len(attachments) == 1 {
						privateURL = spaText(attachments[0]["url"])
					}
				}
			}
			if privateURL == "" || !strings.Contains(page, "configured-private.pdf") || strings.Contains(page, "storage_key") {
				t.Fatal("processed attachment was not privately projected")
			}
			mux := http.NewServeMux()
			s.Media.Register(mux)
			served := httptest.NewRecorder()
			mux.ServeHTTP(served, platform.WithActor(httptest.NewRequest("GET", privateURL, nil), actor))
			if served.Code != 200 || !strings.HasPrefix(served.Body.String(), "%PDF-1.7") || !strings.Contains(served.Header().Get("Content-Disposition"), "attachment") {
				t.Fatal("private screened PDF not downloadable by its member", served.Code)
			}
			stranger := testdb.Actor(t, s.DB, "custom-ttl-stranger-"+submittedTTL, "adult")
			served = httptest.NewRecorder()
			mux.ServeHTTP(served, platform.WithActor(httptest.NewRequest("GET", privateURL, nil), stranger))
			if served.Code != 403 {
				t.Fatal("configured TTL weakened viewer-bound serving", served.Code)
			}
			page, _ = socialLegacyHTML(t, s, stranger, "activity_detail", pk, "")
			if strings.Contains(page, "configured-private.pdf") {
				t.Fatal("configured attachment leaked to a nonmember")
			}
		})
	}
	t.Run("scanner rejection keeps valid TTL private", func(t *testing.T) {
		documents.clean = false
		priorScans := documents.calls
		w := httptest.NewRecorder()
		if !s.SocialAction(w, mediaComposerPDFRequest(t, actor, pk, "7200"), actor, "activity_post") || w.Code != 200 || !strings.Contains(w.Body.String(), "blocked by safety screening") || documents.calls != priorScans+1 {
			t.Fatal("valid TTL bypassed fail-closed document screening", w.Code, documents.calls, priorScans)
		}
		var posts, attachments int
		if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE th.activity_id=$1),(SELECT count(*) FROM media_attachment att JOIN social_post p ON p.id=att.post_id JOIN social_thread th ON th.id=p.thread_id WHERE th.activity_id=$1)`, pk).Scan(&posts, &attachments); err != nil || posts != 4 || attachments != 4 {
			t.Fatal("rejected configured-TTL upload left durable post or attachment", posts, attachments, err)
		}
	})
}
