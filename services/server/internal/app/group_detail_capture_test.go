package app

// This committed producer runs in the COMPLETE canonical App package. It logs
// only a synthetic authorized semantic projection, never an HTTP body, cookie,
// password, CSRF value or renderer nonce. The later body-only comparison uses
// the SAME original widgets and source projection for both rendering engines.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

func groupDetailCaptureValue(t *testing.T, value any, path string) any {
	t.Helper()
	if value == nil {
		return nil
	}
	if safe, ok := value.(*pongo2.Value); ok {
		if safe == nil {
			return nil
		}
		if !strings.HasSuffix(path, ".body_html") && !strings.HasPrefix(path, "projection.post_form.") {
			t.Fatal("unadmitted safe-value source path", path)
		}
		return map[string]any{"_group_detail_capture": "pongo-safe-string", "value": safe.String()}
	}
	// Preserve the original typed-nil footer value: JSON null alone would lose
	// its []string type, which the strict native projection distinguishes.
	if values, ok := value.([]string); ok && values == nil {
		if !strings.HasSuffix(path, ".sentiment_lines") {
			t.Fatal("unadmitted typed-nil string slice", path)
		}
		return map[string]any{"_group_detail_capture": "nil-string-slice"}
	}
	if stamp, ok := value.(time.Time); ok {
		return stamp.Format(time.RFC3339Nano)
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			t.Fatal("nonstring projection map key", path)
		}
		if v.IsNil() {
			return nil
		}
		out := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			lower := strings.ToLower(key)
			if lower == "request" || lower == "authorization" || lower == "token" || strings.Contains(lower, "password") || strings.Contains(lower, "cookie") || strings.Contains(lower, "session") || strings.Contains(lower, "csrf") || strings.Contains(lower, "nonce") {
				t.Fatal("render environment/credential field in semantic capture", path, key)
			}
			out[key] = groupDetailCaptureValue(t, iter.Value().Interface(), path+"."+key)
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = groupDetailCaptureValue(t, v.Index(i).Interface(), path+"["+strconv.Itoa(i)+"]")
		}
		return out
	case reflect.String:
		text := v.String()
		if strings.Contains(text, "/api/media/") {
			t.Fatal("capture fixture must not retain signed media URLs", path)
		}
		return text
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	default:
		t.Fatal("unsupported semantic projection type", path, fmt.Sprintf("%T", value))
		return nil
	}
}

func TestNativeGroupDetailRegisteredProjectionCaptureUsesCurrentAuthority(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	ctx := context.Background()
	group, path := groupDetailAppSeed(t, f, "Generated capture standing group")
	if err := f.app.Social.JoinGroup(ctx, f.peer, group); err != nil {
		t.Fatal(err)
	}
	root, err := f.app.Social.WritePost(ctx, f.owner, "group", group, social.PostInput{Body: "Generated capture **root**"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Social.WritePost(ctx, f.peer, "group", group, social.PostInput{Body: "Generated capture reply", ReplyTo: &root}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Social.WritePost(ctx, f.owner, "group", group, social.PostInput{Body: "Generated capture announcement"}, true); err != nil {
		t.Fatal(err)
	}
	variants := []map[string]any{}
	var actual platform.Actor
	for _, lang := range []string{"en", "ro"} {
		request := httptest.NewRequest(http.MethodGet, "https://app.example"+path, nil)
		request.Header.Set("Accept-Language", lang)
		request.AddCookie(f.credentials.Session)
		request.AddCookie(f.credentials.CSRF)
		w := httptest.NewRecorder()
		f.app.ServeHTTP(w, request)
		body := groupDetailAppHTML(t, w)
		policy := w.Header().Get("Content-Security-Policy-Report-Only")
		match := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(policy)
		if len(match) != 2 || match[1] == "" || !strings.Contains(body, `nonce="`+match[1]+`"`) || !strings.Contains(body, "Generated capture standing group") || !strings.Contains(body, "Generated capture reply") || !strings.Contains(body, "Generated capture announcement") || !strings.Contains(body, `id="thread-chat-config"`) {
			t.Fatal("actual registered GET lost admitted content or nonce/header shape", lang)
		}
		// Recreate the registered GET's current authority from its real cookies.
		// SocialView is the SAME view entry used by the actual router. No fake
		// Actor, session, authority union, alternate query or native VM is used.
		request = httptest.NewRequest(http.MethodGet, "https://app.example"+path, nil)
		request.Header.Set("Accept-Language", lang)
		request.AddCookie(f.credentials.Session)
		request.AddCookie(f.credentials.CSRF)
		actual, err = f.app.authority(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if actual.ID != f.owner.ID || actual.Username != "generated-native-group-owner" || actual.Cohort != "adult" || !actual.IsActive || !actual.IdentityVerified || actual.IsStaff || actual.IsSuperuser {
			t.Fatal("capture actor is not the current synthetic authenticated owner")
		}
		request = platform.WithActor(request, actual)
		request.SetPathValue("pk", strconv.FormatInt(group, 10))
		data, name, handled, err := f.app.Web.SocialView(request, actual, "group_detail")
		if err != nil || !handled || name != "web/group_detail.html" || data["is_owner"] != true || data["is_member"] != true || data["can_post"] != true || data["show_thread"] != true {
			t.Fatal("registered semantic view projection unavailable", err)
		}
		variants = append(variants, map[string]any{"language": lang, "path": path, "projection": groupDetailCaptureValue(t, map[string]any(data), "projection")})
	}
	actor := map[string]any{"id": actual.ID, "public_id": actual.PublicID, "username": actual.Username, "display_name": actual.DisplayName, "age_band": actual.AgeBand, "cohort": actual.Cohort, "role": actual.Role, "identity_verified": actual.IdentityVerified, "is_active": actual.IsActive, "is_staff": actual.IsStaff, "is_superuser": actual.IsSuperuser}
	payload, err := json.Marshal(map[string]any{"schema": 1, "actor": actor, "variants": variants, "render_environment": "EXCLUDED; later QA supplies identical fixed synthetic CSRF/nonce to both body renderers"})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 512<<10 {
		t.Fatal("bounded synthetic semantic marker exceeded")
	}
	// One line, emitted only after BOTH real registered GETs and semantic
	// projections passed. test2json may chunk this line; extractor rejoins it.
	t.Log("NATIVE_GROUP_DETAIL_V1 " + string(payload))
}
