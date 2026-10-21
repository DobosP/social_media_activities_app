package web

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// TI-3: one generic native-output contract instead of per-page needles. Every
// POST form carries exactly one nonempty csrfmiddlewaretoken, every executable
// script carries the response's actual CSP nonce, and nothing relies on inline
// style or inline handlers that the enforced policy (style-src 'self') blocks.

type nativeOutputTag struct {
	name    string
	closing bool
	attrs   map[string]string
}

type nativeOutputCounts struct{ postForms, nonceScripts int }

func nativeOutputIsNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

// nativeOutputTags is a deliberately small tokenizer: comments, declarations and
// raw-text bodies are skipped; attribute names are lower-cased and values
// unescaped. Unterminated markup and duplicate attributes are errors, never
// silently skipped tags.
func nativeOutputTags(body string) ([]nativeOutputTag, error) {
	var tags []nativeOutputTag
	i := 0
	for {
		lt := strings.IndexByte(body[i:], '<')
		if lt < 0 {
			return tags, nil
		}
		i += lt
		if strings.HasPrefix(body[i:], "<!--") {
			end := strings.Index(body[i+4:], "-->")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment at byte %d", i)
			}
			i += 4 + end + 3
			continue
		}
		if strings.HasPrefix(body[i:], "<!") {
			end := strings.IndexByte(body[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("unterminated declaration at byte %d", i)
			}
			i += end + 1
			continue
		}
		j := i + 1
		tag := nativeOutputTag{attrs: map[string]string{}}
		if j < len(body) && body[j] == '/' {
			tag.closing = true
			j++
		}
		start := j
		for j < len(body) && nativeOutputIsNameByte(body[j]) {
			j++
		}
		if j == start || body[start] >= '0' && body[start] <= '9' || body[start] == '-' {
			i++ // A literal '<' in text is not a tag.
			continue
		}
		tag.name = strings.ToLower(body[start:j])
		for {
			for j < len(body) && strings.IndexByte(" \t\r\n\f/", body[j]) >= 0 {
				j++
			}
			if j >= len(body) {
				return nil, fmt.Errorf("unterminated <%s> at byte %d", tag.name, i)
			}
			if body[j] == '>' {
				j++
				break
			}
			nameStart := j
			for j < len(body) && strings.IndexByte(" \t\r\n\f/>=", body[j]) < 0 {
				j++
			}
			attr := strings.ToLower(body[nameStart:j])
			for j < len(body) && strings.IndexByte(" \t\r\n\f", body[j]) >= 0 {
				j++
			}
			value := ""
			if j < len(body) && body[j] == '=' {
				j++
				for j < len(body) && strings.IndexByte(" \t\r\n\f", body[j]) >= 0 {
					j++
				}
				if j < len(body) && (body[j] == '"' || body[j] == '\'') {
					end := strings.IndexByte(body[j+1:], body[j])
					if end < 0 {
						return nil, fmt.Errorf("unterminated %s value on <%s> at byte %d", attr, tag.name, i)
					}
					value = body[j+1 : j+1+end]
					j += end + 2
				} else {
					valueStart := j
					for j < len(body) && strings.IndexByte(" \t\r\n\f>", body[j]) < 0 {
						j++
					}
					value = body[valueStart:j]
				}
			}
			if _, duplicate := tag.attrs[attr]; duplicate {
				return nil, fmt.Errorf("duplicate %s attribute on <%s> at byte %d", attr, tag.name, i)
			}
			tag.attrs[attr] = html.UnescapeString(value)
		}
		tags = append(tags, tag)
		i = j
		if !tag.closing && (tag.name == "script" || tag.name == "style" || tag.name == "textarea" || tag.name == "title") {
			end := strings.Index(strings.ToLower(body[i:]), "</"+tag.name)
			if end < 0 {
				return nil, fmt.Errorf("unterminated <%s> body", tag.name)
			}
			i += end
		}
	}
}

// nativeOutputViolations checks one rendered response body against nonce, the
// script-src nonce of that same response's enforced CSP header.
func nativeOutputViolations(body, nonce string) ([]string, nativeOutputCounts) {
	var violations []string
	var counts nativeOutputCounts
	tags, err := nativeOutputTags(body)
	if err != nil {
		return []string{"unparseable output: " + err.Error()}, counts
	}
	if nonce == "" {
		violations = append(violations, "response has no script-src nonce")
	}
	inForm, postForm, tokens := false, false, 0
	formToken := ""
	for _, tag := range tags {
		if tag.closing {
			if tag.name == "form" {
				if !inForm {
					violations = append(violations, "</form> without an open form")
				} else if postForm && tokens != 1 {
					violations = append(violations, fmt.Sprintf("POST form carries %d csrfmiddlewaretoken inputs", tokens))
				}
				inForm, postForm, tokens = false, false, 0
			}
			continue
		}
		if _, present := tag.attrs["style"]; present {
			violations = append(violations, "inline style attribute on <"+tag.name+">")
		}
		for attr := range tag.attrs {
			if strings.HasPrefix(attr, "on") {
				violations = append(violations, "inline "+attr+" handler on <"+tag.name+">")
			}
			if attr == "formmethod" || attr == "formaction" {
				violations = append(violations, attr+" override on <"+tag.name+">")
			}
		}
		switch tag.name {
		case "style":
			violations = append(violations, "inline <style> element")
		case "script":
			kind := strings.ToLower(strings.TrimSpace(tag.attrs["type"]))
			given, nonced := tag.attrs["nonce"]
			switch kind {
			case "", "text/javascript", "application/javascript", "module", "importmap":
				if !nonced || given != nonce {
					violations = append(violations, fmt.Sprintf("executable <script src=%q> lacks the response nonce", tag.attrs["src"]))
				} else {
					counts.nonceScripts++
				}
			case "application/json", "application/ld+json":
				if nonced && given != nonce {
					violations = append(violations, "data <script> carries a foreign nonce")
				}
			default:
				violations = append(violations, "unclassified <script type="+kind+">")
			}
		case "form":
			if inForm {
				violations = append(violations, "nested <form>")
			}
			inForm, postForm, tokens = true, strings.EqualFold(strings.TrimSpace(tag.attrs["method"]), "post"), 0
			if postForm {
				counts.postForms++
			}
		case "input":
			if tag.attrs["name"] != "csrfmiddlewaretoken" {
				continue
			}
			if !inForm || !postForm {
				violations = append(violations, "csrfmiddlewaretoken outside a POST form")
				continue
			}
			value := tag.attrs["value"]
			if strings.ToLower(tag.attrs["type"]) != "hidden" || value == "" {
				violations = append(violations, "csrfmiddlewaretoken is not a nonempty hidden input")
				continue
			}
			if formToken == "" {
				formToken = value
			} else if value != formToken {
				violations = append(violations, "POST forms on one page carry different CSRF tokens")
			}
			tokens++
		}
	}
	if inForm {
		violations = append(violations, "unterminated <form>")
	}
	return violations, counts
}

var nativeOutputNoncePattern = regexp.MustCompile(`'nonce-([^'\s]+)'`)

func nativeOutputHeaderNonce(header http.Header) string {
	policy := header.Get("Content-Security-Policy")
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 || fields[0] != "script-src" {
			continue
		}
		if found := nativeOutputNoncePattern.FindAllStringSubmatch(directive, -1); len(found) == 1 {
			return found[0][1]
		}
	}
	return ""
}

type nativeOutputState struct{ body, nonce string }

func nativeOutputRender(t *testing.T, renderer *Renderer, request *http.Request, name string, data pongo2.Context, language string) nativeOutputState {
	t.Helper()
	renderer.CSPEnforce = true
	delete(data, "request") // The ordinary renderer owns the actual response nonce.
	data["language"] = language
	if _, selector := data["current_language"]; selector {
		data["current_language"] = language // The settings language selector names the shown language.
	}
	if language == "ro" {
		data["display_theme"], data["display_text"], data["display_motion"] = "dark", "large", "reduce"
	}
	request.Header.Set("Accept-Language", language)
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, name, data); err != nil {
		t.Fatal(name, language, err)
	}
	if response.Code != http.StatusOK {
		t.Fatal(name, language, "ordinary HTML transport unavailable", response.Code)
	}
	return nativeOutputState{body: response.Body.String(), nonce: nativeOutputHeaderNonce(response.Header())}
}

// Group-detail variants: owner with the live thread and presend nudge, a teen
// reader on the live thread, a nonmember and an outside-cohort curator.
var nativeOutputGroupDetailVariants = map[string]func(*http.Request, pongo2.Context) *http.Request{
	"owner-live": func(request *http.Request, _ pongo2.Context) *http.Request { return request },
	"teen-reader-live": func(request *http.Request, data pongo2.Context) *http.Request {
		actor, _ := platform.ActorFrom(request)
		actor.ID, actor.Cohort = 14, "teen"
		data["user"] = socialActor(actor)
		data["group"].(map[string]any)["cohort"] = "teen"
		data["is_owner"], data["can_announce"], data["can_post"], data["show_dissent_concern"] = false, false, false, false
		data["roster"], data["announcements"], data["feed"] = nil, nil, []map[string]any{}
		data["posts"] = []map[string]any{groupDetailFixturePost(91, groupDetailFixturePerson(15, "Generated minor peer"), "Current minor body", "teen")}
		data["thread_chat_config"] = groupDetailFixtureConfig(41, 51, actor.ID)
		return platform.WithActor(request, actor)
	},
	"nonmember": func(request *http.Request, data pongo2.Context) *http.Request {
		actor, _ := platform.ActorFrom(request)
		actor.ID = 12
		data["user"] = socialActor(actor)
		data["is_owner"], data["is_member"], data["show_thread"], data["can_post"], data["can_announce"], data["show_dissent_concern"] = false, false, false, false, false, false
		data["roster"], data["posts"], data["announcements"] = nil, nil, nil
		delete(data, "thread_chat_config")
		delete(data, "presend_nudge")
		return platform.WithActor(request, actor)
	},
	"outside-cohort-curator": func(request *http.Request, data pongo2.Context) *http.Request {
		actor, _ := platform.ActorFrom(request)
		actor.IsStaff = true
		data["user"] = socialActor(actor)
		g := data["group"].(map[string]any)
		g["cohort"], g["is_staff_curated"], g["owner_is_staff"] = "child", true, true
		data["is_member"], data["can_post"], data["show_dissent_concern"], data["roster"], data["posts"], data["feed"] = false, false, false, nil, nil, []map[string]any{}
		delete(data, "thread_chat_config")
		delete(data, "presend_nudge")
		return platform.WithActor(request, actor)
	},
}

type nativeOutputFixture func() (*Renderer, *http.Request, string, pongo2.Context)

// The remaining native page renderers, each from its own existing fixture.
func nativeOutputRendererFixtures(t *testing.T) map[string]nativeOutputFixture {
	t.Helper()
	named := func(name string, fixture func() (*Renderer, *http.Request, pongo2.Context)) nativeOutputFixture {
		return func() (*Renderer, *http.Request, string, pongo2.Context) {
			renderer, request, data := fixture()
			return renderer, request, name, data
		}
	}
	plain := func(name string, fixture func() (*http.Request, pongo2.Context)) nativeOutputFixture {
		return named(name, func() (*Renderer, *http.Request, pongo2.Context) {
			request, data := fixture()
			return NewRenderer(t.TempDir()), request, data
		})
	}
	mode := func(fixture func(string) (*http.Request, pongo2.Context), value string) func() (*http.Request, pongo2.Context) {
		return func() (*http.Request, pongo2.Context) { return fixture(value) }
	}
	account := func(settings bool) nativeOutputFixture {
		return func() (*Renderer, *http.Request, string, pongo2.Context) {
			return accountCenterFixture(t, settings, false)
		}
	}
	displayAccess := func(display, actionError bool) nativeOutputFixture {
		return func() (*Renderer, *http.Request, string, pongo2.Context) {
			return displayAccessFixture(t, display, true, actionError)
		}
	}
	return map[string]nativeOutputFixture{
		"you":               account(false),
		"settings":          account(true),
		"display":           displayAccess(true, false),
		"access":            displayAccess(false, true),
		"topics":            named("web/topic_preferences.html", func() (*Renderer, *http.Request, pongo2.Context) { return topicsNativeFixture(t) }),
		"group-form":        plain("web/group_form.html", func() (*http.Request, pongo2.Context) { return groupCreateProjectionFixture(false) }),
		"group-form-staff":  plain("web/group_form.html", func() (*http.Request, pongo2.Context) { return groupCreateProjectionFixture(true) }),
		"wards":             plain("web/wards.html", familyProjectionFixture),
		"inbox-mixed":       plain("web/notifications.html", mode(notificationInboxFixture, "mixed-en")),
		"inbox-empty":       plain("web/notifications.html", mode(notificationInboxFixture, "empty-en")),
		"preferences-some":  plain("web/notification_preferences.html", mode(notificationPreferencesFixture, "some-ro")),
		"preferences-error": plain("web/notification_preferences.html", mode(notificationPreferencesFixture, "errors-en")),
	}
}

func nativeOutputStates(t *testing.T) map[string]nativeOutputState {
	t.Helper()
	states := map[string]nativeOutputState{}
	names := make([]string, 0, len(completionReferenceNames))
	for name := range completionReferenceNames {
		names = append(names, name)
	}
	sort.Strings(names)
	renderers := nativeOutputRendererFixtures(t)
	for _, language := range []string{"en", "ro"} {
		for _, name := range names {
			renderer, request, data := completionPreviewFixture(t, name)
			states[name+"-"+language] = nativeOutputRender(t, renderer, request, name, data, language)
		}
		for variant, change := range nativeOutputGroupDetailVariants {
			renderer, request, data := groupDetailProjectionFixture(t)
			request = change(request, data)
			states["web/group_detail.html:"+variant+"-"+language] = nativeOutputRender(t, renderer, request, "web/group_detail.html", data, language)
		}
		for variant, fixture := range renderers {
			renderer, request, name, data := fixture()
			states[name+":"+variant+"-"+language] = nativeOutputRender(t, renderer, request, name, data, language)
		}
	}
	if len(states) != 2*(len(completionReferenceNames)+len(nativeOutputGroupDetailVariants)+len(renderers)) || len(completionReferenceNames) != 39 {
		t.Fatal("every completion preview state and native renderer variant in both languages required")
	}
	return states
}

func TestNativeOutputFormsScriptsAndStylesHoldTheCSPContract(t *testing.T) {
	states := nativeOutputStates(t)
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var total nativeOutputCounts
	for _, key := range keys {
		violations, counts := nativeOutputViolations(states[key].body, states[key].nonce)
		for _, violation := range violations {
			t.Errorf("%s: %s", key, violation)
		}
		total.postForms += counts.postForms
		total.nonceScripts += counts.nonceScripts
	}
	t.Logf("%d states, %d POST forms, %d nonced executable scripts", len(states), total.postForms, total.nonceScripts)
	// Floors keep a fixture regression from passing vacuously.
	if total.postForms < len(states) || total.nonceScripts < 2*len(states) {
		t.Fatalf("contract checked too little output: %d POST forms, %d nonced scripts over %d states", total.postForms, total.nonceScripts, len(states))
	}
	live := states["web/group_detail.html:owner-live-en"]
	for _, script := range []string{"/static/js/presend-nudge.js", "/static/js/thread-chat.js"} {
		if !strings.Contains(live.body, `<script nonce="`+live.nonce+`" src="`+script+`"></script>`) {
			t.Fatal("live group thread script is not bound to the response nonce", script)
		}
	}
}

// Each single-defect mutation of real current output must be caught, so the
// contract above can never pass by checking nothing.
func TestNativeOutputContractRejectsEachSingleDefect(t *testing.T) {
	renderer, request, data := groupDetailProjectionFixture(t)
	state := nativeOutputRender(t, renderer, request, "web/group_detail.html", data, "en")
	if violations, counts := nativeOutputViolations(state.body, state.nonce); len(violations) != 0 || counts.postForms < 5 || counts.nonceScripts < 4 {
		t.Fatal("unmutated live group detail must be clean and exercise forms and scripts", violations, counts)
	}
	token := `<input type="hidden" name="csrfmiddlewaretoken" value="synthetic-group-detail-csrf">`
	mutations := map[string][2]string{
		"drop-one-token":      {token, ""},
		"inject-style":        {`<h1`, `<h1 style="color:red"`},
		"unnonce-thread-chat": {`<script nonce="` + state.nonce + `" src="/static/js/thread-chat.js">`, `<script src="/static/js/thread-chat.js">`},
		"foreign-nonce":       {`nonce="` + state.nonce + `" src="/static/js/presend-nudge.js"`, `nonce="stale" src="/static/js/presend-nudge.js"`},
		"inline-handler":      {`<h1`, `<h1 onclick="x()"`},
		"inline-style-block":  {`</head>`, `<style>h1{color:red}</style></head>`},
		"empty-token":         {token, `<input type="hidden" name="csrfmiddlewaretoken" value="">`},
		"second-token":        {token, token + token},
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(state.body, mutation[0]) {
				t.Fatal("mutation anchor missing from current output", mutation[0])
			}
			mutated := strings.Replace(state.body, mutation[0], mutation[1], 1)
			if violations, _ := nativeOutputViolations(mutated, state.nonce); len(violations) == 0 {
				t.Fatal("contract accepted a single-defect mutation")
			}
		})
	}
}
