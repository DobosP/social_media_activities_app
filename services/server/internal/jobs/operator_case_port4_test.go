package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestOperatorCase4DueContextOneCryptoIDPerRunAndNoCallerLeak(t *testing.T) {
	ctx := context.Background()
	if ops.DueRunID(ctx) != "-" {
		t.Fatal("outside-run correlation marker changed")
	}
	r := New(nil, DefaultConfig())
	seen := []string{}
	for _, name := range DueNames {
		r.handlers[name] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
			seen = append(seen, ops.DueRunID(ctx))
			return 0, nil
		}
	}
	prior := ""
	for run := 0; run < 2; run++ {
		seen = nil
		if rows, err := r.RunDue(ctx); err != nil || len(rows) != len(DueNames) || len(seen) != len(DueNames) {
			t.Fatal("real due registry did not carry all handler contexts", err)
		}
		id := seen[0]
		if !strings.HasPrefix(id, "job:run_due_jobs:") {
			t.Fatal("due correlation prefix changed")
		}
		decoded, err := hex.DecodeString(strings.TrimPrefix(id, "job:run_due_jobs:"))
		if err != nil || len(decoded) != 16 || len(strings.TrimPrefix(id, "job:run_due_jobs:")) != 32 {
			t.Fatal("due correlation is not exact crypto16-byte/32hex")
		}
		for _, got := range seen {
			if got != id {
				t.Fatal("one due run split across multiple correlation IDs")
			}
		}
		if id == prior || ops.DueRunID(ctx) != "-" {
			t.Fatal("successive runs reused IDs or mutated caller correlation")
		}
		prior = id
	}
}

func TestOperatorCase4TypedRefusalBoundsIdentityAndFixedReasonPrivacy(t *testing.T) {
	for _, count := range []int64{0, 12, maxProductRefusalWithheld} {
		err := productRefusal(count, refusalSchemaNotReady|refusalPolicyDrift|refusalUnknownCondition)
		var refused *ProductRefusalError
		if !errors.Is(err, ErrProductRefused) || !errors.As(err, &refused) || refused.Withheld() != count || len(refused.Reasons()) != 3 {
			t.Fatal("typed refusal identity/count/fixed reason bound changed")
		}
		if len(err.Error()) > 250 {
			t.Fatal("fixed refusal diagnostic grew without bound")
		}
		if count == 12 && !strings.Contains(err.Error(), "withheld 12 item(s)") {
			t.Fatal("source withheld count diagnostic missing")
		}
		reasons := refused.Reasons()
		reasons[0] = "synthetic-secret-marker"
		if strings.Contains(refused.Error(), "synthetic-secret-marker") || refused.Reasons()[0] != "schema not ready" {
			t.Fatal("caller mutated fixed reason metadata")
		}
	}
	for _, count := range []int64{-1, maxProductRefusalWithheld + 1} {
		err := productRefusal(count, 0)
		if err == nil || errors.Is(err, ErrProductRefused) {
			t.Fatal("invalid aggregate refusal count masqueraded as source refusal")
		}
	}
	if publicRefusalCondition("schema not ready") != refusalSchemaNotReady || publicRefusalCondition("policy ruleset drifted") != refusalPolicyDrift || publicRefusalCondition("synthetic-secret-marker token=private") != refusalUnknownCondition {
		t.Fatal("public condition whitelist changed")
	}
}

func TestOperatorCase4RefusalReaderExactCountersKnownAndUnknownNotes(t *testing.T) {
	zero := retirementPackPage(t)
	zero["items"], zero["withheld"] = []any{}, 0
	if got, err := retirementReadPages(t, []map[string]any{zero}); err != nil || !got.Complete || len(got.Items) != 0 {
		t.Fatal("genuinely empty zero-withheld product became a refusal")
	}
	maximum := retirementPackPage(t)
	maximum["items"], maximum["withheld"] = []any{}, 1000000
	_, maximumErr := retirementReadPages(t, []map[string]any{maximum})
	var maximumRefusal *ProductRefusalError
	if !errors.As(maximumErr, &maximumRefusal) || maximumRefusal.Withheld() != 1000000 {
		t.Fatal("original inclusive per-page maximum count was narrowed")
	}
	for _, name := range []string{"withheld12", "policy_note", "unknown_note", "two_pages", "valid_items_preserved"} {
		t.Run(name, func(t *testing.T) {
			page := retirementPackPage(t)
			page["items"] = []any{}
			page["withheld"] = 12
			if name == "policy_note" {
				page["withheld"] = 0
				page["errors"] = []any{"policy ruleset drifted"}
			}
			if name == "unknown_note" {
				page["errors"] = []any{"synthetic-secret-marker token=private"}
			}
			pages := []map[string]any{page}
			if name == "two_pages" {
				next := retirementPackPage(t)
				next["items"] = []any{}
				page["withheld"], next["withheld"] = 5, 7
				page["pagination"].(map[string]any)["next_cursor"] = "count-second"
				pages = append(pages, next)
			}
			if name == "valid_items_preserved" {
				page["items"] = retirementPackPage(t)["items"]
			}
			got, err := retirementReadPages(t, pages)
			if name == "valid_items_preserved" {
				if err != nil || got.Complete || len(got.Items) != 2 {
					t.Fatal("refusal projection changed valid partial item arithmetic")
				}
				return
			}
			var refused *ProductRefusalError
			if !errors.Is(err, ErrProductRefused) || !errors.As(err, &refused) || got.Complete || len(got.Items) != 0 {
				t.Fatal("all-withheld/refused producer identity changed", err)
			}
			want := int64(12)
			if name == "policy_note" {
				want = 0
			}
			if refused.Withheld() != want {
				t.Fatal("page withheld arithmetic changed", refused.Withheld(), want)
			}
			if name == "policy_note" && !strings.Contains(err.Error(), "policy ruleset drifted") {
				t.Fatal("source-supported fixed diagnostic missing")
			}
			if strings.Contains(err.Error(), "synthetic-secret-marker") || strings.Contains(err.Error(), "token=private") {
				t.Fatal("arbitrary producer note escaped typed refusal")
			}
		})
	}
	for _, value := range []any{true, -1, .5, json.Number("9223372036854775808"), 1000001} {
		page := retirementPackPage(t)
		page["items"] = []any{}
		page["withheld"] = value
		if _, err := retirementReadPages(t, []map[string]any{page}); err == nil || errors.Is(err, ErrProductRefused) {
			t.Fatal("malformed per-page count crossed original validation bound")
		}
	}
}

func TestPostgresOperatorCase4SyncRefusalSafeCountDiagnosticAndUnknownMarker(t *testing.T) {
	for _, condition := range []string{"schema not ready", "synthetic-secret-marker token=private"} {
		t.Run(condition, func(t *testing.T) {
			r := jobFixture(t)
			page := retirementPackPage(t)
			page["items"] = []any{}
			page["withheld"] = 12
			page["errors"] = []any{condition}
			raw, _ := json.Marshal(page)
			r.Config.RoeduSyncEnabled = true
			r.Config.Roedu = &RoeduClient{BaseURL: "https://producer.invalid", APIKey: "synthetic-case4-only", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}}
			covers := 0
			r.Config.ResolvePlaceCovers = func(context.Context, map[string]json.RawMessage) (any, error) { covers++; return 0, nil }
			out, err := r.SyncRoedu(context.Background())
			if err != nil || covers != 1 || out["refused"] != true || out["refreshed"] != false || out["withheld"] != int64(12) {
				t.Fatal("refusal count projection changed shared tick or cover isolation", out, err)
			}
			body, _ := json.Marshal(out)
			if !bytes.Contains(body, []byte("withheld 12 item(s)")) || condition == "schema not ready" && !bytes.Contains(body, []byte("schema not ready")) {
				t.Fatal("fixed known condition/count missing from actual summary")
			}
			if bytes.Contains(body, []byte("synthetic-secret-marker")) || bytes.Contains(body, []byte("token=private")) {
				t.Fatal("arbitrary producer note reached stdout-shaped summary")
			}
			var count int
			var audit []byte
			if err := r.DB.QueryRow(context.Background(), `SELECT count(*) FROM safety_auditlog WHERE event='ingestion.roedu_refused'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("refusal audit missing", err)
			}
			if err := r.DB.QueryRow(context.Background(), `SELECT data FROM safety_auditlog WHERE event='ingestion.roedu_refused'`).Scan(&audit); err != nil || bytes.Contains(audit, []byte(condition)) || bytes.Contains(audit, []byte("withheld")) {
				t.Fatal("refusal projection copied note/count into audit metadata", err)
			}
		})
	}
}

func TestOperatorCase4CommonsFileTitleExactSourceLadder(t *testing.T) {
	for _, tc := range []struct {
		tags  map[string]any
		title string
	}{
		{map[string]any{"wikimedia_commons": "File:Central_Park.jpg"}, "File:Central_Park.jpg"},
		{map[string]any{"wikimedia_commons": "Category:Parks"}, ""},
		{map[string]any{"image": "https://example.com/x.jpg"}, ""},
		{map[string]any{}, ""},
		{map[string]any{"image": "https://commons.wikimedia.org/wiki/File:Parcul_Central.jpg"}, "File:Parcul_Central.jpg"},
	} {
		if got := CommonsFileTitle(tc.tags); got != tc.title {
			t.Fatal("Commons title ladder differs", got, tc.title)
		}
	}
}

func TestOperatorCase4PlaceNameNormalizationAndSimilarity(t *testing.T) {
	if string(normalizePlaceName("Café Central!")) != "cafe central" || string(normalizePlaceName("  Multiple   Spaces ")) != "multiple spaces" {
		t.Fatal("source name normalization changed")
	}
	if PlaceNameSimilarity("Central Library", "central library.") != 1 || PlaceNameSimilarity("Central Library", "City Pool") >= .5 {
		t.Fatal("source name similarity thresholds changed")
	}
}

func TestPostgresOperatorCase4DuplicateCandidateExactGeoAndNameCases(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	place := testdb.Place(t, r.DB, "Central Library", "osm")
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET location=ST_SetSRID(ST_MakePoint(23.5900,46.7700),4326),osm_type='node',osm_id=1 WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		lon, lat float64
		want     int64
	}{{"Central Library", 23.59005, 46.77002, place}, {"Central Library", 23.61, 46.77, 0}, {"Pizza Place", 23.59005, 46.77002, 0}} {
		var got int64
		err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
			var e error
			got, e = findRoeduDuplicate(ctx, tx, map[string]any{"title": tc.name, "location": map[string]any{"lon": tc.lon, "lat": tc.lat}})
			return e
		})
		if err != nil || got != tc.want {
			t.Fatal("source close/similar versus far/dissimilar candidate changed", got, tc.want, err)
		}
		if got != 0 {
			var osm int64
			if err := r.DB.QueryRow(ctx, `SELECT osm_id FROM places_place WHERE id=$1`, got).Scan(&osm); err != nil || osm != 1 {
				t.Fatal("matched source venue lost expected OSM identity", err)
			}
		}
	}
}

func TestOperatorCase4CommonsNoImageinfoOrUnsupportedMIMENeverDownloads(t *testing.T) {
	for _, name := range []string{"missing", "unsupported"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			c := &CommonsClient{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Hostname() != "commons.wikimedia.org" {
					t.Fatal("missing/unsupported metadata downloaded an asset")
				}
				body := `{"query":{"pages":{"1":{}}}}`
				if name == "unsupported" {
					body = `{"query":{"pages":{"1":{"imageinfo":[{"thumbmime":"image/svg+xml","thumburl":"https://upload.wikimedia.org/Logo.svg"}]}}}}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			got, err := c.Image(context.Background(), "File:Missing.jpg")
			if err != nil || got != nil || calls != 1 {
				t.Fatal("missing/unsupported imageinfo was admitted", err, calls)
			}
		})
	}
}

type operatorCase4CleanScanner struct{}

func (operatorCase4CleanScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{Clean: true}, nil
}

func operatorCase4CoverFixture(t *testing.T) (*Runner, *media.Service, *media.LocalStore, []byte, *int) {
	t.Helper()
	r := jobFixture(t)
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit", "avifenc"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatal("required native codec missing", bin)
		}
	}
	processor, err := media.NewProcessor(media.DefaultConfig(t.TempDir()), operatorCase4CleanScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := media.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	service := media.NewService(r.DB, processor, blobs, media.TokenCodec{Key: []byte(strings.Repeat("s", 32))}, r.Config.Social)
	r.Config.ImportCover = service.ImportLicensedPlaceCover
	im := image.NewRGBA(image.Rect(0, 0, 128, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 128; x++ {
			im.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, im, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := encoded.Bytes()
	calls := new(int)
	r.Config.Commons = &CommonsClient{TempDir: t.TempDir(), HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		*calls++
		var body []byte
		if req.URL.Hostname() == "commons.wikimedia.org" {
			if req.URL.Query().Get("action") == "wbgetclaims" {
				if req.URL.Query().Get("entity") != "Q123" || req.URL.Query().Get("property") != "P18" {
					t.Fatal("Wikidata P18 fallback request changed")
				}
				body = []byte(`{"claims":{"P18":[{"mainsnak":{"datavalue":{"value":"Parcul_Central.jpg"}}}]}}`)
			} else {
				body = []byte(`{"query":{"pages":{"1":{"imageinfo":[{"thumburl":"https://upload.wikimedia.org/thumb/Central_Park.jpg/800px-Central_Park.jpg","thumbmime":"image/jpeg","descriptionurl":"https://commons.wikimedia.org/wiki/File:Central_Park.jpg","extmetadata":{"Artist":{"value":"<a href=\"https://example.org\">Ana Pop</a>"},"LicenseShortName":{"value":"CC BY-SA 4.0"}}}]}}}}`)
			}
		} else if req.URL.Hostname() == "upload.wikimedia.org" {
			body = raw
		} else {
			t.Fatal("unexpected external transport destination")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}}
	return r, service, blobs, raw, calls
}

func operatorCase4CoverPlace(t *testing.T, r *Runner, name string, tags map[string]any) int64 {
	t.Helper()
	id := testdb.Place(t, r.DB, name, "osm")
	raw, err := json.Marshal(tags)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(context.Background(), `UPDATE places_place SET raw_tags=$2 WHERE id=$1`, id, raw); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgresOperatorCase4CoverCommandCheckedDryNoCandidateAndP18(t *testing.T) {
	for _, name := range []string{"resolve_marks_checked", "already_checked", "dry_run", "wikidata_p18"} {
		t.Run(name, func(t *testing.T) {
			r, _, _, _, calls := operatorCase4CoverFixture(t)
			ctx := context.Background()
			tags := map[string]any{"wikimedia_commons": "File:Central_Park.jpg"}
			if name == "already_checked" {
				tags["cover_checked"] = true
			}
			if name == "wikidata_p18" {
				tags = map[string]any{"wikidata": "Q123"}
			}
			place := operatorCase4CoverPlace(t, r, "Parcul Central", tags)
			noRef := operatorCase4CoverPlace(t, r, "Fara referinta", map[string]any{})
			_, err := r.ResolveCoversWithOptions(ctx, CoverResolveOptions{Limit: 200, DryRun: name == "dry_run"})
			if err != nil {
				t.Fatal(err)
			}
			var covers int
			var checked bool
			if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placecover WHERE place_id=$1`, place).Scan(&covers); err != nil {
				t.Fatal(err)
			}
			if err := r.DB.QueryRow(ctx, `SELECT raw_tags?'cover_checked' FROM places_place WHERE id=$1`, place).Scan(&checked); err != nil {
				t.Fatal(err)
			}
			if name == "already_checked" || name == "dry_run" {
				if *calls != 0 || covers != 0 || checked != (name == "already_checked") {
					t.Fatal("checked/dry run contacted provider or changed fixture state")
				}
			} else if covers != 1 || !checked || *calls != map[bool]int{false: 2, true: 3}[name == "wikidata_p18"] {
				t.Fatal("cover resolution ladder or checked marker changed", covers, *calls)
			}
			if err := r.DB.QueryRow(ctx, `SELECT raw_tags?'cover_checked' FROM places_place WHERE id=$1`, noRef).Scan(&checked); err != nil || checked {
				t.Fatal("no-reference place was falsely marked checked", err)
			}
			if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placecover WHERE place_id=$1`, noRef).Scan(&covers); err != nil || covers != 0 {
				t.Fatal("no-reference place acquired a cover", err)
			}
		})
	}
}

func TestPostgresOperatorCase4LicensedCoverCanonicalStorageAndServingProof(t *testing.T) {
	r, service, blobs, original, _ := operatorCase4CoverFixture(t)
	ctx := context.Background()
	place := operatorCase4CoverPlace(t, r, "Parcul Central", map[string]any{"wikimedia_commons": "File:Central_Park.jpg"})
	if _, err := r.ResolveCoversWithOptions(ctx, CoverResolveOptions{Limit: 200}); err != nil {
		t.Fatal(err)
	}
	var cover, size int64
	var source, mime, attribution, license, page, key, digest string
	var stripped bool
	if err := r.DB.QueryRow(ctx, `SELECT id,source,content_type,byte_size,attribution,license_name,source_page_url,storage_key,sha256,exif_stripped FROM places_placecover WHERE place_id=$1`, place).Scan(&cover, &source, &mime, &size, &attribution, &license, &page, &key, &digest, &stripped); err != nil {
		t.Fatal(err)
	}
	if source != "wikimedia" || mime != "image/avif" || attribution != "Ana Pop, CC BY-SA 4.0, via Wikimedia Commons" || license != "CC BY-SA 4.0" || page != "https://commons.wikimedia.org/wiki/File:Central_Park.jpg" || !stripped {
		t.Fatal("canonical transformation lost original relevant provenance or canonical metadata")
	}
	url, err := service.PlaceCoverURL(ctx, place)
	if err != nil || !strings.HasPrefix(url, "/api/media/place-cover-file/") {
		t.Fatal("public signed cover URL missing", err)
	}
	mux := http.NewServeMux()
	service.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	stored := w.Body.Bytes()
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/avif" || int64(len(stored)) != size || size == 0 || bytes.Equal(stored, original) {
		t.Fatal("actual stored and served canonical bytes/size/header proof failed")
	}
	if len(stored) < 32 || string(stored[4:8]) != "ftyp" || !bytes.Contains(stored[8:32], []byte("avif")) {
		t.Fatal("served bytes are not independently identifiable canonical AVIF")
	}
	sum := sha256.Sum256(stored)
	if hex.EncodeToString(sum[:]) != digest {
		t.Fatal("stored SHA256 did not bind actual served bytes")
	}
	if physical, err := blobs.Size(ctx, key); err != nil || physical != size {
		t.Fatal("private storage size did not match row and served bytes", err)
	}
	var manifest []byte
	if err := r.DB.QueryRow(ctx, `SELECT payload FROM media_go_manifest WHERE kind='place-cover' AND row_id=$1`, cover).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	var recorded media.Manifest
	if json.Unmarshal(manifest, &recorded) != nil {
		t.Fatal("invalid native canonical manifest")
	}
	acquired := sha256.Sum256(original)
	if recorded.SourceSHA256 != hex.EncodeToString(acquired[:]) || recorded.Main.SHA256 != digest || recorded.Main.ByteSize != size || recorded.Main.ContentType != mime {
		t.Fatal("canonical manifest lost exact acquired byte provenance or transformed identity")
	}
	probeFile, err := os.CreateTemp(t.TempDir(), "served-*.avif")
	if err != nil {
		t.Fatal(err)
	}
	path := probeFile.Name()
	if _, err = probeFile.Write(stored); err != nil {
		t.Fatal(err)
	}
	if err = probeFile.Close(); err != nil {
		t.Fatal(err)
	}
	// Probe the served object independently, beyond its MIME and ftyp marker.
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	probe, err := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width,height", "-of", "json", path).Output()
	if err != nil || !bytes.Contains(probe, []byte(`"codec_name": "av1"`)) {
		t.Fatal("actual served canonical AVIF is not decodable", err)
	}
}

func TestPostgresOperatorCase4CommonsWillNotReplaceExistingCover(t *testing.T) {
	r, _, blobs, _, calls := operatorCase4CoverFixture(t)
	ctx := context.Background()
	place := operatorCase4CoverPlace(t, r, "Parcul Central", map[string]any{"wikimedia_commons": "File:Central_Park.jpg"})
	if _, err := r.ResolveCoversWithOptions(ctx, CoverResolveOptions{Limit: 200}); err != nil {
		t.Fatal(err)
	}
	var id int64
	var key string
	if err := r.DB.QueryRow(ctx, `SELECT id,storage_key FROM places_placecover WHERE place_id=$1`, place).Scan(&id, &key); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(ctx, `UPDATE places_placecover SET source='business' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	before := *calls
	if _, err := r.ResolveCoversWithOptions(ctx, CoverResolveOptions{Limit: 200, Recheck: true}); err != nil {
		t.Fatal(err)
	}
	var retained int64
	var retainedKey, source string
	if err := r.DB.QueryRow(ctx, `SELECT id,storage_key,source FROM places_placecover WHERE place_id=$1`, place).Scan(&retained, &retainedKey, &source); err != nil || retained != id || retainedKey != key || source != "business" || *calls != before {
		t.Fatal("existing business cover identity changed or contacted provider", err)
	}
	if size, err := blobs.Size(ctx, key); err != nil || size == 0 {
		t.Fatal("existing cover physically removed", err)
	}
}
