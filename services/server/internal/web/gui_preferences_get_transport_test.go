package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// This is current-current registered transport, not an original oracle or an
// authentication/privacy decision. Both mux calls execute the real typed
// view -> page -> Render path independently. There is no captured RenderPage
// callback, shared-context equality claim, template rewrite or local HTML mask.
var guiPreferencesGETTransport = flag.Bool("gui-preferences-get-transport", false, "explicit current synthetic adult/public preference GET OS/FS transport; existing isolated web DSN/original15/released normalizer required; no capture")

//go:embed testdata/gui-preferences-get/registry.json
var guiPreferencesGETRegistryBytes []byte

const guiPreferencesGETRegistrySHA = "f16c607713b95f0210d1a7cc044aa5c2d56ea82c082a00274f9859d63620ec40"

type guiPreferenceGETCase struct {
	guiPublicGetCase
	Location string
}

type guiPreferenceGETRegistry struct {
	Schema  int
	Scope   string
	Cases   []guiPreferenceGETCase
	Sources map[string]guiG1Binding
}

type guiPreferenceGETPair struct {
	item  guiPreferenceGETCase
	disk  guiCurrentTransportResponse
	files guiCurrentTransportResponse
	actor platform.Actor
}

var guiPreferencesGETFiles = []string{
	"apps/web/templates/web/_you_tabs.html",
	"apps/web/templates/web/access_preferences.html",
	"apps/web/templates/web/display_preferences.html",
	"apps/web/templates/web/notification_preferences.html",
	"apps/web/templates/web/topic_preferences.html",
	"locale/ro/LC_MESSAGES/django.po",
	"templates/base.html",
}

func guiPreferenceGETRegistryCheck(value guiPreferenceGETRegistry) error {
	var expected guiPreferenceGETRegistry
	if guiPublicHash(guiPreferencesGETRegistryBytes) != guiPreferencesGETRegistrySHA || guiG1StrictJSON(guiPreferencesGETRegistryBytes, &expected) != nil || !reflect.DeepEqual(value, expected) || value.Schema != 1 || value.Scope != "current-native-registered-preference-GET-transport-only" || len(value.Cases) != 13 || len(value.Sources) != 138 {
		return fmt.Errorf("preference-registry")
	}
	seen, HTML, negative := map[string]bool{}, 0, 0
	for _, item := range value.Cases {
		if item.ID == "" || seen[item.ID] || item.Path != routeURL(item.Route) || item.Language != "en" || item.Profile != "adult" && item.Profile != "anonymous" || item.JSONLD || item.Filtered || len(item.Contains) != 0 {
			return fmt.Errorf("preference-registry-route")
		}
		seen[item.ID] = true
		if item.Status == http.StatusOK {
			HTML++
			if item.Template != "web/"+item.Route+".html" || item.Location != "" {
				return fmt.Errorf("preference-registry-HTML")
			}
		} else {
			negative++
			if item.Status != http.StatusFound || item.Profile != "anonymous" || publicPages[item.Route] || item.Template != "" || item.Location != "/login/?next="+url.QueryEscape(item.Path) {
				return fmt.Errorf("preference-registry-refusal")
			}
		}
	}
	if HTML != 10 || negative != 3 {
		return fmt.Errorf("preference-registry-count")
	}
	return nil
}

func guiPreferenceGETInputs(root string, registry guiPreferenceGETRegistry) (map[string]guiG1Binding, error) {
	raw, err := guiPublicBoundFile(filepath.Join(root, "services/server/internal/web/testdata/gui-preferences-get/registry.json"), 1<<20)
	if err != nil || !bytes.Equal(raw, guiPreferencesGETRegistryBytes) || guiPreferenceGETRegistryCheck(registry) != nil {
		return nil, fmt.Errorf("preference-registry-source")
	}
	actual := map[string]guiG1Binding{}
	for name, wanted := range registry.Sources {
		_, row, err := guiCurrentTransportReadBinding(root, name, 2<<20)
		if err != nil || !guiPublicGetBindingLive(row, wanted) {
			return nil, fmt.Errorf("preference-current-source")
		}
		actual[name] = row
	}
	return actual, nil
}

func guiPreferenceGETSnapshot(root string, registry guiPreferenceGETRegistry) (fstest.MapFS, error) {
	files := fstest.MapFS{}
	for _, name := range guiPreferencesGETFiles {
		raw, binding, err := guiCurrentTransportReadBinding(root, name, 2<<20)
		if err != nil || !guiPublicGetBindingLive(binding, registry.Sources[name]) || files[name] != nil {
			return nil, fmt.Errorf("preference-template-source")
		}
		files[name] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: 0o444}
	}
	return files, nil
}

func guiPreferenceGETSnapshotCheck(root string, registry guiPreferenceGETRegistry, files fstest.MapFS) error {
	if len(files) != len(guiPreferencesGETFiles) {
		return fmt.Errorf("preference-template-set")
	}
	for _, name := range guiPreferencesGETFiles {
		file := files[name]
		raw, binding, err := guiCurrentTransportReadBinding(root, name, 2<<20)
		if err != nil || !guiPublicGetBindingLive(binding, registry.Sources[name]) || file == nil || file.Mode != 0o444 || !bytes.Equal(raw, file.Data) {
			return fmt.Errorf("preference-template-bytes")
		}
	}
	return nil
}

func guiPreferenceGETPrerequisites(dsn string, tool *guiPublicNormalizer) error {
	if guiPublicGetRequireDSN(dsn) != nil {
		return fmt.Errorf("preference-DSN-required")
	}
	if tool == nil {
		return fmt.Errorf("preference-normalizer-required")
	}
	return nil
}

// Seeding changes only this fresh isolated fixture's fictional adult rows. It
// does not exercise POST/settings/admission or invent an empty notification GET.
func guiPreferenceGETSeed(t *testing.T, s *Server, actor platform.Actor, item guiPreferenceGETCase) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"places_accesspreference", "notifications_notificationpreference", "recommendations_topicpreference"} {
		if _, err := s.DB.Exec(ctx, "DELETE FROM "+table+" WHERE user_id=$1", actor.ID); err != nil {
			t.Fatal("owned preference fixture reset refused")
		}
	}
	if item.Route == "access_preferences" && item.Profile == "adult" {
		step, toilet, hearing, quiet := item.State != "none", item.State == "all", item.State == "all", item.State != "none"
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_accesspreference(user_id,needs_step_free,needs_accessible_toilet,needs_hearing_loop,prefers_quiet,created_at,updated_at) VALUES($1,$2,$3,$4,$5,now(),now())`, actor.ID, step, toilet, hearing, quiet); err != nil {
			t.Fatal("owned access fixture refused")
		}
	}
	if item.Route == "notification_preferences" && item.Profile == "adult" {
		muted := []string{}
		if item.State == "mixed" {
			muted = []string{"announcement", "event_reminder"}
		} else if item.State == "arrival" {
			muted = []string{"arrival"}
		}
		if _, err := s.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,$2)`, actor.ID, muted); err != nil {
			t.Fatal("owned notification fixture refused")
		}
	}
	if item.Route == "topic_preferences" && item.State == "selected" && item.Profile == "adult" {
		tag, err := s.DB.Exec(ctx, `INSERT INTO recommendations_topicpreference(user_id,category_id,created_at) SELECT $1,id,now() FROM taxonomy_activitycategory WHERE slug='reading' AND parent_id IS NULL`, actor.ID)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatal("exact owned reading-topic fixture required")
		}
	}
}

func guiPreferenceGETState(t *testing.T, s *Server, actor platform.Actor) []byte {
	t.Helper()
	var raw []byte
	// Exact private fixture before/after bytes only; no rows/SQL/credentials are
	// printed or emitted. Counts protect unrelated reads from becoming writes.
	err := s.DB.QueryRow(context.Background(), `SELECT jsonb_build_object('access',(SELECT to_jsonb(p) FROM places_accesspreference p WHERE user_id=$1),'muted',(SELECT to_jsonb(p) FROM notifications_notificationpreference p WHERE user_id=$1),'topics',(SELECT jsonb_agg(to_jsonb(p) ORDER BY category_id) FROM recommendations_topicpreference p WHERE user_id=$1),'users',(SELECT count(*) FROM accounts_user),'avatars',(SELECT count(*) FROM accounts_signatureavatar),'notices',(SELECT count(*) FROM notifications_notification),'audits',(SELECT count(*) FROM safety_auditlog))`, actor.ID).Scan(&raw)
	if err != nil || len(raw) == 0 {
		t.Fatal("owned preference fixture readset unavailable")
	}
	return raw
}

func guiPreferenceGET(t *testing.T, s *Server, files fstest.MapFS, item guiPreferenceGETCase, actor platform.Actor, filesystem bool, requests *int) guiCurrentTransportResponse {
	t.Helper()
	renderer := NewRenderer(s.Config.Root)
	if filesystem {
		var err error
		renderer, err = NewRendererFS(s.Config.Root, files)
		if err != nil {
			t.Fatal("current preference FS constructor refused")
		}
	}
	renderer.CSPEnforce = s.Config.CSPEnforce
	trace := &guiPublicTraceLoader{delegate: renderer.loader}
	renderer.set = pongo2.NewSet("native-social-current-preference", trace)
	s.Renderer = renderer
	mux := http.NewServeMux()
	s.Register(mux)
	request := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+item.Path, nil)
	if item.Profile == "adult" {
		request = platform.WithActor(request, actor)
	}
	request.AddCookie(&http.Cookie{Name: "django_language", Value: "en"})
	request.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("g", 52)})
	if item.State == "contrast" {
		for name, value := range map[string]string{"display_theme": "contrast", "display_text": "larger", "display_motion": "reduce"} {
			request.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}
	response := httptest.NewRecorder()
	*requests = *requests + 1
	mux.ServeHTTP(response, request)
	return guiCurrentTransportResponse{response, append([]guiPublicTemplateLoad(nil), trace.loads...), s.Config.PublicURL + item.Path}
}

func guiPreferenceGETContent(body []byte, item guiPreferenceGETCase) error {
	text := string(body)
	if item.Route == "display_preferences" {
		for name, values := range map[string][]string{"display_theme": {"auto", "light", "dark", "contrast"}, "display_text": {"normal", "large", "larger"}, "display_motion": {"auto", "reduce", "full"}} {
			selected := map[string]string{"display_theme": "auto", "display_text": "normal", "display_motion": "auto"}[name]
			if item.State == "contrast" {
				selected = map[string]string{"display_theme": "contrast", "display_text": "larger", "display_motion": "reduce"}[name]
			}
			for _, value := range values {
				tag := `name="` + name + `" value="` + value + `"`
				if value == selected {
					tag += " checked"
				}
				if strings.Count(text, tag+">") != 1 {
					return fmt.Errorf("preference-display-choice")
				}
			}
		}
		if strings.Count(text, `aria-label="Account"`) != map[bool]int{false: 0, true: 1}[item.Profile == "adult"] {
			return fmt.Errorf("preference-display-account-arm")
		}
		return nil
	}
	start := strings.Index(text, `<form method="post" class="card">`)
	if start < 0 || strings.Count(text, `<form method="post" class="card">`) != 1 {
		return fmt.Errorf("preference-owning-form")
	}
	end := strings.Index(text[start:], "</form>")
	if end < 0 {
		return fmt.Errorf("preference-owning-form")
	}
	form := text[start : start+end+len("</form>")]
	if strings.Count(form, `name="csrfmiddlewaretoken" value="`+strings.Repeat("g", 52)+`"`) != 1 {
		return fmt.Errorf("preference-form-CSRF")
	}
	choices := []string{}
	checked := map[string]bool{}
	switch item.Route {
	case "access_preferences":
		choices = []string{"needs_step_free", "needs_accessible_toilet", "needs_hearing_loop", "prefers_quiet"}
		for _, choice := range choices {
			checked[choice] = item.State == "all" || item.State == "mixed" && (choice == "needs_step_free" || choice == "prefers_quiet")
		}
		if !strings.Contains(form, "doesn't change what you see.") || !strings.Contains(text, "places with unknown accessibility are still shown to you.") {
			return fmt.Errorf("preference-access-copy")
		}
	case "notification_preferences":
		choices = accountNotificationOrder
		if len(choices) != 22 || !strings.Contains(text, "Important safety and moderation notices are always delivered") || !strings.Contains(form, "alerts for a child you supervise.") {
			return fmt.Errorf("preference-notification-fixed-order")
		}
		for _, choice := range choices {
			checked[choice] = item.State == "arrival" && choice == "arrival" || item.State == "mixed" && (choice == "announcement" || choice == "event_reminder")
		}
	case "topic_preferences":
		choices = []string{"culture", "fitness", "outdoor", "reading", "social", "sport", "tabletop", "video_games"}
		checked["reading"] = item.State == "selected"
		if !strings.Contains(text, "It never hides anything:") || strings.Contains(form, "No topics are available yet.") || strings.Count(form, ">see your feed</a>") != map[bool]int{false: 0, true: 1}[item.State == "selected"] {
			return fmt.Errorf("preference-topic-source-arms")
		}
	default:
		return fmt.Errorf("preference-unowned-route")
	}
	if strings.Count(form, `type="checkbox"`) != len(choices) {
		return fmt.Errorf("preference-choice-count")
	}
	position := -1
	for _, choice := range choices {
		tag := `type="checkbox" name="` + choice + `"`
		if item.Route == "notification_preferences" {
			tag = `type="checkbox" name="muted" value="` + choice + `"`
		} else if item.Route == "topic_preferences" {
			tag = `type="checkbox" name="topics" value="` + choice + `"`
		}
		if checked[choice] {
			tag += " checked"
		}
		tag += ">"
		next := strings.Index(form, tag)
		if next <= position || strings.Count(form, tag) != 1 {
			return fmt.Errorf("preference-choice-order-state")
		}
		position = next
	}
	return nil
}

func guiPreferenceGETResponse(item guiPreferenceGETCase, response guiCurrentTransportResponse, registry guiPreferenceGETRegistry, actor platform.Actor) (string, error) {
	if response.recorder == nil || response.recorder.Code != item.Status || response.recorder.Header().Get("Location") != item.Location || response.recorder.Header().Get("Allow") != "" || len(response.recorder.Body.Bytes()) == 0 || response.recorder.Body.Len() > 2<<20 {
		return "", fmt.Errorf("preference-response-status")
	}
	if item.Status != 200 {
		if len(response.loads) != 0 || response.recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.recorder.Header().Get("Content-Security-Policy-Report-Only") != "" || response.recorder.Header().Get("Content-Security-Policy") != "" || len(response.recorder.Result().Cookies()) != 0 {
			return "", fmt.Errorf("preference-negative-render")
		}
		return "", nil
	}
	if response.recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.recorder.Header().Get("Cache-Control") != "private, no-cache" || response.recorder.Header().Get("Content-Security-Policy") != "" || response.recorder.Header().Get("Vary") != "Cookie" || response.canonical != "https://fixture.local"+item.Path {
		return "", fmt.Errorf("preference-HTTP-metadata")
	}
	wanted := map[string]bool{item.Template: true, "base.html": true}
	if item.Route == "display_preferences" {
		wanted["web/_you_tabs.html"] = true
	}
	if len(response.loads) != len(wanted) || len(response.loads) == 0 || response.loads[0].Name != item.Template {
		return "", fmt.Errorf("preference-consumed-template-set")
	}
	for _, load := range response.loads {
		name := "apps/web/templates/" + load.Name
		if load.Name == "base.html" {
			name = "templates/base.html"
		}
		if !wanted[load.Name] {
			return "", fmt.Errorf("preference-consumed-template-set")
		}
		delete(wanted, load.Name)
		raw, err := guiPublicBoundFile(filepath.Join(guiPreferenceGETRoot(), filepath.FromSlash(name)), 2<<20)
		if err != nil || guiPublicHash(raw) != registry.Sources[name].SHA256 {
			return "", fmt.Errorf("preference-consumed-template-source")
		}
		transformed := []byte(transformTemplate(string(raw)))
		if load.Bytes != len(transformed) || load.SHA256 != guiPublicHash(transformed) {
			return "", fmt.Errorf("preference-consumed-template-source")
		}
	}
	if item.Profile == "adult" {
		prefix := `class="avatar nav-avatar" src="data:image/svg+xml;base64,`
		body := response.recorder.Body.String()
		start := strings.Index(body, prefix)
		if start < 0 || strings.Count(body, prefix) != 1 || strings.Index(body[start+len(prefix):], `"`) <= 0 {
			return "", fmt.Errorf("preference-own-avatar-presentation")
		}
	}
	if err := guiPreferenceGETContent(response.recorder.Body.Bytes(), item); err != nil {
		return "", err
	}
	var owner *string
	if item.Profile == "adult" {
		owner = &actor.PublicID
	}
	binding, err := guiPublicScriptNonceBinding(response.recorder.Body.String(), response.recorder.Header().Get("Content-Security-Policy-Report-Only"), owner)
	if err != nil {
		return "", fmt.Errorf("preference-nonce-binding")
	}
	nonce, ok := binding["header_nonce_sha256"].(string)
	if !ok || len(nonce) != 64 {
		return "", fmt.Errorf("preference-nonce-binding")
	}
	return nonce, nil
}

func guiPreferenceGETRoot() string {
	root, _ := filepath.Abs("../../../..")
	return root
}

func guiPreferenceGETPairCheck(pair guiPreferenceGETPair, registry guiPreferenceGETRegistry) error {
	left, err := guiPreferenceGETResponse(pair.item, pair.disk, registry, pair.actor)
	if err != nil {
		return err
	}
	right, err := guiPreferenceGETResponse(pair.item, pair.files, registry, pair.actor)
	if err != nil {
		return err
	}
	first, second := pair.disk.recorder.Header().Clone(), pair.files.recorder.Header().Clone()
	// Raw per-response nonces remain checked above; only the independently
	// generated nonce-bearing header cannot be byte-compared across two GETs.
	first.Del("Content-Security-Policy-Report-Only")
	second.Del("Content-Security-Policy-Report-Only")
	if !reflect.DeepEqual(first, second) || pair.disk.canonical != pair.files.canonical {
		return fmt.Errorf("preference-response-headers")
	}
	if pair.item.Status == 200 {
		if left == right {
			return fmt.Errorf("preference-nonce-freshness")
		}
	} else if !bytes.Equal(pair.disk.recorder.Body.Bytes(), pair.files.recorder.Body.Bytes()) {
		return fmt.Errorf("preference-negative-body")
	}
	return nil
}

func TestGUIPreferencesRegisteredGETTransport(t *testing.T) {
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil || guiPreferenceGETRegistryCheck(registry) != nil {
		t.Fatal("closed preference registry refused")
	}
	root := guiPreferenceGETRoot()
	before, err := guiPreferenceGETInputs(root, registry)
	if err != nil {
		t.Fatal("current preference source138 refused")
	}
	files, err := guiPreferenceGETSnapshot(root, registry)
	if err != nil {
		t.Fatal("current preference template7 refused")
	}
	t.Cleanup(func() {
		after, err := guiPreferenceGETInputs(root, registry)
		if err != nil || !reflect.DeepEqual(before, after) || guiPreferenceGETSnapshotCheck(root, registry, files) != nil {
			t.Error("preference source/template changed")
		}
	})
	for _, name := range []string{"missing", "duplicate", "reordered", "actor", "route", "status"} {
		t.Run("refuse-registry-"+name, func(t *testing.T) {
			raw, err := json.Marshal(registry)
			if err != nil {
				t.Fatal("owned registry clone unavailable")
			}
			var changed guiPreferenceGETRegistry
			if guiG1StrictJSON(raw, &changed) != nil {
				t.Fatal("owned registry clone refused")
			}
			switch name {
			case "missing":
				changed.Cases = changed.Cases[:12]
			case "duplicate":
				changed.Cases[1] = changed.Cases[0]
			case "reordered":
				changed.Cases[0], changed.Cases[1] = changed.Cases[1], changed.Cases[0]
			case "actor":
				changed.Cases[0].Profile = "adult"
			case "route":
				changed.Cases[0].Path = "/settings/"
			case "status":
				changed.Cases[5].Status = 200
			}
			if guiPreferenceGETRegistryCheck(changed) == nil || guiPreferenceGETRegistryCheck(registry) != nil {
				t.Fatal("changed registry admitted or source aliased")
			}
		})
	}
	if !*guiPreferencesGETTransport {
		t.Log("HTTP NOT REQUESTED: source138/registry13/six refusals only; no PG, current GET, normalization or adoption qualification")
		return
	}
	if *guiPublicGetWalkOutput != "" || *guiPublicGoldenOutput != "" || *guiPublicGoldenFSOutput != "" || *guiPublicGetWalkNormalizeReference != "" || *guiPublicCurrentGetTransport {
		t.Fatal("preference transport refuses mixed capture/receiver/current-G1 modes")
	}
	tool, err := guiPublicNewNormalizer(root)
	if err != nil || guiPreferenceGETPrerequisites(*webDomainDSN, tool) != nil {
		t.Fatal("actual preference DSN/released normalizer required before fixture; no skip")
	}
	checkpoint, err := guiPublicBoundFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"), 1<<20)
	if err != nil {
		t.Fatal("original15 checkpoint unavailable")
	}
	original, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
	if err != nil {
		t.Fatal("actual original15 custody refused")
	}
	t.Cleanup(func() {
		next, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
		if err != nil || !reflect.DeepEqual(original, next) || tool.checkUnchanged() != nil {
			t.Error("original15/released tool inputs changed")
		}
	})
	s, actor, _, _, _ := webCasePortFixture(t)
	if s.Config.Root != root || s.Config.SPA || s.Config.CSPEnforce || actor.Cohort != "adult" || !actor.IsActive || s.Accounts == nil || s.API == nil {
		t.Fatal("existing isolated adult native fixture required")
	}
	api, apiCalls := s.API, []string{}
	s.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls = append(apiCalls, r.Method+" "+r.URL.Path)
		api.ServeHTTP(w, r) // Observe the real API; no fake settings response.
	})
	requests, pairs, HTML, negative := 0, 0, 0, 0
	var baseline, refusal *guiPreferenceGETPair
	var canonical []byte
	for _, item := range registry.Cases {
		t.Run("current-preference-"+item.ID, func(t *testing.T) {
			guiPreferenceGETSeed(t, s, actor, item)
			state, apiStart := guiPreferenceGETState(t, s, actor), len(apiCalls)
			pair := guiPreferenceGETPair{item: item, actor: actor}
			pair.disk = guiPreferenceGET(t, s, files, item, actor, false, &requests)
			pair.files = guiPreferenceGET(t, s, files, item, actor, true, &requests)
			if !bytes.Equal(state, guiPreferenceGETState(t, s, actor)) {
				t.Fatal("registered preference GET changed owned rows")
			}
			wanted := []string{}
			if item.Status == 200 && (item.Route == "access_preferences" || item.Route == "notification_preferences") {
				wanted = []string{"GET /api/accounts/me/settings/", "GET /api/accounts/me/settings/"}
			}
			if !reflect.DeepEqual(apiCalls[apiStart:], wanted) {
				t.Fatal("actual settings API read path/count differs")
			}
			if err := guiPreferenceGETPairCheck(pair, registry); err != nil {
				t.Fatal("actual registered preference pair refused: " + err.Error())
			}
			pairs++
			if item.Status != 200 {
				negative++
				if refusal == nil {
					copy := pair
					refusal = &copy
				}
				t.Logf("current preference case=%s status=%d native_negative_raw_equal=true os_sha256=%s fs_sha256=%s; no HTML normalization", item.ID, item.Status, guiPublicHash(pair.disk.recorder.Body.Bytes()), guiPublicHash(pair.files.recorder.Body.Bytes()))
				return
			}
			left, leftHard, leftErr := tool.normalize(pair.disk.recorder.Body.Bytes())
			right, rightHard, rightErr := tool.normalize(pair.files.recorder.Body.Bytes())
			if leftErr != nil || rightErr != nil || len(leftHard) != 0 || len(rightHard) != 0 {
				t.Fatalf("strict preference normalization refused: os_hard=%d fs_hard=%d os_codes=%v fs_codes=%v", len(leftHard), len(rightHard), guiG1HardCodes(leftHard), guiG1HardCodes(rightHard))
			}
			if err := guiCurrentTransportCanonicalAgreement(left, right); err != nil {
				t.Fatal("whole current canonical preference response differs")
			}
			HTML++
			if baseline == nil {
				copy := pair
				baseline, canonical = &copy, append([]byte(nil), left...)
			}
			t.Logf("current preference case=%s status=200 os_sha256=%s fs_sha256=%s os_canonical_sha256=%s fs_canonical_sha256=%s hard0=true; independent real typed callbacks, no shared-context or original semantic claim", item.ID, guiPublicHash(pair.disk.recorder.Body.Bytes()), guiPublicHash(pair.files.recorder.Body.Bytes()), guiPublicHash(left), guiPublicHash(right))
		})
	}
	for _, name := range []string{"missing-dsn", "missing-normalizer", "status", "consumed-source", "negative-location", "negative-body", "canonical-difference", "hard-finding"} {
		t.Run("refuse-current-preference-"+name, func(t *testing.T) {
			if guiPreferenceGETPrerequisites(*webDomainDSN, tool) != nil {
				t.Fatal("actual prerequisites must pass before refusal")
			}
			if name == "missing-dsn" {
				if err := guiPreferenceGETPrerequisites("", tool); err == nil || err.Error() != "preference-DSN-required" {
					t.Fatal("missing DSN admitted")
				}
				return
			}
			if name == "missing-normalizer" {
				if err := guiPreferenceGETPrerequisites(*webDomainDSN, nil); err == nil || err.Error() != "preference-normalizer-required" {
					t.Fatal("missing normalizer admitted")
				}
				return
			}
			selected := baseline
			if strings.HasPrefix(name, "negative-") {
				selected = refusal
			}
			if selected == nil || guiPreferenceGETPairCheck(*selected, registry) != nil {
				t.Fatal("genuine current paired control unavailable")
			}
			unchanged := guiPublicHash(selected.files.recorder.Body.Bytes())
			changed := *selected
			changed.files = guiCurrentTransportClone(selected.files)
			switch name {
			case "status":
				changed.files.recorder.Code = 404
			case "consumed-source":
				changed.files.loads[0].SHA256 = strings.Repeat("0", 64)
			case "negative-location":
				changed.files.recorder.Header().Set("Location", "/synthetic-other/")
			case "negative-body":
				changed.files.recorder.Body.WriteString("synthetic-changed-refusal")
			case "canonical-difference":
				raw, err := guiG1ReplaceOnce(selected.files.recorder.Body.Bytes(), "<h1>Display settings</h1>", "<h1>Synthetic changed heading</h1>")
				if err != nil {
					t.Fatal("unique actual canonical mutation target unavailable")
				}
				value, hard, err := tool.normalize(raw)
				if err != nil || len(hard) != 0 || guiCurrentTransportCanonicalAgreement(canonical, value) == nil {
					t.Fatal("strict normalized text mutation not detected")
				}
			case "hard-finding":
				_, hard, err := tool.normalize(append(append([]byte(nil), selected.files.recorder.Body.Bytes()...), []byte(`<a href="">Synthetic invalid URL</a>`)...))
				if err == nil || len(hard) == 0 || !strings.Contains(strings.Join(guiG1HardCodes(hard), ","), "empty-url") {
					t.Fatal("strict actual empty-URL hard finding not detected")
				}
			}
			if name != "canonical-difference" && name != "hard-finding" && guiPreferenceGETPairCheck(changed, registry) == nil {
				t.Fatal("changed current observation admitted")
			}
			if unchanged != guiPublicHash(selected.files.recorder.Body.Bytes()) || guiPreferenceGETPairCheck(*selected, registry) != nil {
				t.Fatal("mutation aliased actual accepted control")
			}
		})
	}
	if requests != 26 || pairs != 13 || HTML != 10 || negative != 3 || len(apiCalls) != 12 {
		t.Errorf("incomplete current preference inventory: requests=%d pairs=%d HTML=%d negative=%d settingsAPI=%d", requests, pairs, HTML, negative, len(apiCalls))
	}
	t.Logf("current preference observations requests=%d paired_HTTP=%d strict_HTML=%d native_negative=%d settings_API_reads=%d; expected26/13/10/3/12, no original oracle/authprivacy/adoption/browser/full qualification", requests, pairs, HTML, negative, len(apiCalls))
}
