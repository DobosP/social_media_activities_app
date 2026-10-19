package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

var familyBenchmarkSink []byte

// Snapshot of the existing common Renderer.Render context preparation. Prepared
// outside BOTH timed loops: nonce generation, language/default merge, authorized
// source query, HTTP headers, network, compression and asset fetching are excluded.
// Shared helper closures and the original source catalogue remain real, not mocks.
func familyBenchmarkDefaults(r *Renderer, request *http.Request, data pongo2.Context) pongo2.Context {
	nonce := "synthetic-family-benchmark-nonce"
	lang := language(request)
	actor, authenticated := platform.ActorFrom(request)
	defaults := pongo2.Context{"language": lang, "LANGUAGE_CODE": lang, "display_theme": display(request, "display_theme", "auto", "auto", "light", "dark", "contrast"), "display_text": display(request, "display_text", "normal", "normal", "large", "larger"), "display_motion": display(request, "display_motion", "auto", "auto", "reduce", "full"), "site_name": "Social Activities", "canonical_url": request.URL.Path, "og_locale": "en_GB", "request": map[string]any{"csp_nonce": nonce, "path": request.URL.Path, "GET": request.URL.Query()}, "user": map[string]any{"is_authenticated": authenticated, "is_moderator": actor.Moderator(), "is_admin": actor.Admin(), "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": data["avatar_uri"]}, "activity_svg": activitySVG, "route": routeURL, "translate": func(message string) string { return r.catalog.translate(lang, message, 1) }, "translate_block": func(encoded string, count any, values ...any) *pongo2.Value {
		return translatedBlock(r.catalog, lang, encoded, spaInt(count), values...)
	}, "json_script": func(value any, id string) *pongo2.Value { return jsonScript(value, id, nonce) }, "spa_assets": func(entry string) *pongo2.Value { return r.assets(entry, nonce) }, "available_langs": []any{[]string{"en", "English"}, []string{"ro", "Română"}}, "static": func(s string) string { return "/static/" + strings.TrimPrefix(s, "/") }}
	for key, value := range data {
		defaults[key] = value
	}
	return defaults
}

func familyBenchmarkFixture(b *testing.B, lang string) (*http.Request, pongo2.Context, string) {
	b.Helper()
	request, data := familyProjectionFixture()
	if actor, ok := platform.ActorFrom(request); !ok || !actor.IsActive {
		b.Fatal("authorized fixture actor unavailable")
	}
	request.Header.Set("Accept-Language", lang)
	data["language"] = lang
	// Exact AccountView presentation aliases omitted by the projector-only
	// fixture. Both paths receive this SAME context; no private data is added.
	for _, ward := range data["wards"].([]map[string]any) {
		ward["pk"] = ward["id"]
		ward["get_cohort_display"] = map[string]string{"child": "Child (under 16)", "teen": "Teen (16-17)", "adult": "Adult (18+)"}[ward["cohort"].(string)]
		caps := ward["caps"].(map[string]any)
		var rawDays strings.Builder
		for _, day := range caps["guardrail_allowed_weekday_ints"].([]int) {
			rawDays.WriteString(strconv.Itoa(day))
		}
		caps["guardrail_allowed_weekdays"] = rawDays.String()
	}
	raw, err := json.Marshal(data)
	if err != nil {
		b.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return request, data, hex.EncodeToString(sum[:])
}

func familyBenchmarkGzip(b *testing.B, raw []byte) int {
	b.Helper()
	var out bytes.Buffer
	writer, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		b.Fatal(err)
	}
	if _, err = writer.Write(raw); err != nil {
		b.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		b.Fatal(err)
	}
	return out.Len()
}

// Body-render-only comparison of the SAME authorized synthetic two-ward
// snapshot. Pongo is the unchanged original on-disk family/base templates;
// native includes strict VM/copy construction and parse-once template execution.
// This does not measure full response latency, database work, browser behavior,
// equivalent markup, or production speedup. Different intentional HTML/JS assets
// are visible in body byte metrics rather than treated as equivalent output.
func BenchmarkNativeFamilyBodyRender(b *testing.B) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		b.Fatal(err)
	}
	for _, lang := range []string{"en", "ro"} {
		b.Run(lang, func(b *testing.B) {
			renderer := NewRenderer(root)
			request, data, fixtureHash := familyBenchmarkFixture(b, lang)
			merged := familyBenchmarkDefaults(renderer, request, data)
			const name = "web/wards.html"
			old, err := renderer.set.FromCache(name)
			if err != nil {
				b.Fatal("original cached template unavailable", err)
			}
			var oldWarm bytes.Buffer
			if err = old.ExecuteWriter(merged, &oldWarm); err != nil {
				b.Fatal("original warm render", err)
			}
			nativeWarm, err := renderer.renderFamily(request, name, merged)
			if err != nil {
				b.Fatal("native warm render", err)
			}
			if oldWarm.Len() == 0 || len(nativeWarm) == 0 {
				b.Fatal("empty matched fixture body")
			}
			b.Logf("body-only shared synthetic fixture sha256=%s; no full-response or speedup acceptance claim", fixtureHash)
			oldBytes, nativeBytes := oldWarm.Len(), len(nativeWarm)
			oldGzip, nativeGzip := familyBenchmarkGzip(b, oldWarm.Bytes()), familyBenchmarkGzip(b, nativeWarm)
			b.Run("original-pongo-cached", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					parsed, err := renderer.set.FromCache(name)
					if err != nil {
						b.Fatal(err)
					}
					var out bytes.Buffer
					if err = parsed.ExecuteWriter(merged, &out); err != nil {
						b.Fatal(err)
					}
					familyBenchmarkSink = out.Bytes()
				}
				b.StopTimer()
				b.ReportMetric(float64(oldBytes), "html-B/op")
				b.ReportMetric(float64(oldGzip), "gzip9-B/op")
			})
			b.Run("native-projection-and-cached-template", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					raw, err := renderer.renderFamily(request, name, merged)
					if err != nil {
						b.Fatal(err)
					}
					familyBenchmarkSink = raw
				}
				b.StopTimer()
				b.ReportMetric(float64(nativeBytes), "html-B/op")
				b.ReportMetric(float64(nativeGzip), "gzip9-B/op")
			})
		})
	}
}
