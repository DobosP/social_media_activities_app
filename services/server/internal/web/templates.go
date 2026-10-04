package web

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/flosch/pongo2/v6"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed routes.json
var routesJSON []byte
var routePaths = func() map[string]string {
	var result map[string]string
	_ = json.Unmarshal(routesJSON, &result)
	return result
}()
var converter = regexp.MustCompile(`<[^:>]+:([^>]+)>`)

func routeURL(name string, args ...any) string {
	path, ok := routePaths[name]
	if !ok {
		switch name {
		case "set_language":
			return "/i18n/setlang/"
		case "admin:index":
			return "/admin/"
		}
		return "#"
	}
	i := 0
	path = converter.ReplaceAllStringFunc(path, func(_ string) string {
		if i >= len(args) {
			return ""
		}
		v := url.PathEscape(fmt.Sprint(args[i]))
		i++
		return v
	})
	return "/" + path
}

var words = regexp.MustCompile(`(?:"[^"]*"|'[^']*'|[^\s"']+)(?:"[^"]*"|'[^']*'|[^\s"']+)*`)
var translationBlock = regexp.MustCompile(`(?s)\{%\s*blocktrans(?:late)?\b([^%]*)%\}(.*?)\{%\s*endblocktrans(?:late)?\s*%\}`)
var interpolation = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
var transAssign = regexp.MustCompile(`\{%\s*(?:trans|translate)\s+((?:"[^"]*"|'[^']*'))\s+as\s+(\w+)\s*%\}`)
var customTag = regexp.MustCompile(`\{%\s*(nonced_json_script|spa_entry|activity_accent|get_available_languages|get_language_info_list)\s+([^%]*)%\}`)

func transformTemplate(s string) string {
	s = regexp.MustCompile(`(?s)\{#.*?#\}`).ReplaceAllString(s, "")
	s = loadTag.ReplaceAllString(s, "")
	s = translationBlock.ReplaceAllStringFunc(s, func(tag string) string {
		m := translationBlock.FindStringSubmatch(tag)
		params := strings.TrimSpace(m[1])
		body := m[2]
		pairs := words.FindAllString(params, -1)
		bindings := []string{}
		count := ""
		for i, word := range pairs {
			if word == "with" || word == "count" {
				continue
			}
			if strings.Contains(word, "=") {
				bindings = append(bindings, word)
				if i > 0 && pairs[i-1] == "count" {
					count = strings.SplitN(word, "=", 2)[0]
				}
			}
		}
		parts := regexp.MustCompile(`\{%\s*plural\s*%\}`).Split(body, 2)
		render := func(raw string) string {
			counter := "1"
			if count != "" {
				counter = count
			}
			expressions := []string{}
			message := interpolation.ReplaceAllStringFunc(raw, func(match string) string {
				expr := interpolation.FindStringSubmatch(match)[1]
				expressions = append(expressions, expr)
				name := strings.TrimSpace(strings.Split(expr, "|")[0])
				return "%(" + name + ")s"
			})
			args := ""
			if len(expressions) > 0 {
				args = ", " + strings.Join(expressions, ", ")
			}
			return "{{ translate_block(" + strconv.Quote(base64.RawStdEncoding.EncodeToString([]byte(message))) + ", " + counter + args + ") }}"
		}
		transformed := render(parts[0])
		if len(parts) == 2 && count != "" {
			transformed = "{% if " + count + " == 1 %}" + transformed + "{% else %}" + render(parts[1]) + "{% endif %}"
		}
		if len(bindings) > 0 {
			transformed = "{% with " + strings.Join(bindings, " ") + " %}" + transformed + "{% endwith %}"
		}
		return transformed
	})
	s = transAssign.ReplaceAllString(s, "{% set $2 = translate($1) %}")
	s = transTag.ReplaceAllString(s, "{{ translate($1) }}")
	s = langTag.ReplaceAllString(s, "{% set $1 = language %}")
	s = staticTag.ReplaceAllString(s, "{{ static($1) }}")
	s = urlTag.ReplaceAllStringFunc(s, func(tag string) string {
		m := urlTag.FindStringSubmatch(tag)
		args := words.FindAllString(strings.TrimSpace(m[2]), -1)
		assigned := ""
		if len(args) >= 2 && args[len(args)-2] == "as" {
			assigned = args[len(args)-1]
			args = args[:len(args)-2]
		}
		for i, arg := range args {
			if eq := strings.Index(arg, "="); eq >= 0 {
				args[i] = arg[eq+1:]
			}
		}
		expr := "route(" + m[1]
		if len(args) > 0 {
			expr += ", " + strings.Join(args, ", ")
		}
		expr += ")"
		if assigned != "" {
			return "{% set " + assigned + " = " + expr + " %}"
		}
		return "{{ " + expr + " }}"
	})
	s = customTag.ReplaceAllStringFunc(s, func(tag string) string {
		m := customTag.FindStringSubmatch(tag)
		args := words.FindAllString(strings.TrimSpace(m[2]), -1)
		switch m[1] {
		case "nonced_json_script":
			return "{{ json_script(" + strings.Join(args, ", ") + ") }}"
		case "spa_entry":
			return "{{ spa_assets(" + strings.Join(args, ", ") + ") }}"
		case "activity_accent":
			return "{{ activity_svg(" + strings.Join(args, ", ") + ") }}"
		case "get_available_languages":
			if len(args) == 2 && args[0] == "as" {
				return "{% set " + args[1] + " = available_langs %}"
			}
		case "get_language_info_list":
			if len(args) >= 4 && args[len(args)-2] == "as" {
				return "{% set " + args[len(args)-1] + " = language_infos %}"
			}
		}
		return ""
	})
	s = strings.ReplaceAll(s, "{% csrf_token %}", `<input type="hidden" name="csrfmiddlewaretoken" value="{{ csrf }}">`)
	s = strings.ReplaceAll(s, "block.super", "block.Super")
	// Bound fields are data maps in Go. Their widget is already escaped/safe;
	// retain lower-case attributes for label/help/error access in the old layouts.
	s = interpolation.ReplaceAllStringFunc(s, func(match string) string {
		expression := strings.TrimSpace(interpolation.FindStringSubmatch(match)[1])
		if expression == "field" {
			return "{{ field.widget }}"
		}
		parts := strings.Split(expression, ".")
		if len(parts) == 2 && (parts[0] == "form" || strings.HasSuffix(parts[0], "_form")) {
			for _, fields := range definitions {
				if _, ok := fields[parts[1]]; ok {
					return "{{ " + expression + ".widget }}"
				}
			}
		}
		return match
	})
	s = strings.ReplaceAll(s, "forloop.", "forloop.")
	s = regexp.MustCompile(`(?s)\{%.*?%\}|\{\{.*?\}\}`).ReplaceAllStringFunc(s, func(tag string) string {
		tag = regexp.MustCompile(`([^\s{}]+) not in ([^\s%}]+)`).ReplaceAllString(tag, "not ($1 in $2)")
		tag = strings.ReplaceAll(tag, " is not ", " != ")
		tag = strings.ReplaceAll(tag, " is ", " == ")
		for _, pair := range [][2]string{{"None", "nil"}, {"True", "true"}, {"False", "false"}} {
			tag = regexp.MustCompile(`\b`+pair[0]+`\b`).ReplaceAllString(tag, pair[1])
		}
		return strings.ReplaceAll(tag, `\T`, `\\T`)
	})
	s = regexp.MustCompile(`(\{%\s*for\s+[^%]+\s+in\s+)(page|groups_page)(\s*%\})`).ReplaceAllString(s, "$1$2.object_list$3")
	return tupleLoops(s)
}
func translateBlock(message string, values ...any) *pongo2.Value {
	// The trusted release template supplies markup; every interpolated value is
	// HTML-escaped before insertion, including quotes inside attribute contexts.
	for _, v := range values {
		at := strings.Index(message, "%s")
		if at < 0 {
			break
		}
		message = message[:at] + html.EscapeString(fmt.Sprint(v)) + message[at+2:]
	}
	return pongo2.AsSafeValue(message)
}
func jsonScript(value any, id, nonce string) *pongo2.Value {
	raw, err := json.Marshal(value)
	if err != nil {
		return pongo2.AsSafeValue("")
	}
	return pongo2.AsSafeValue(`<script nonce="` + html.EscapeString(nonce) + `" id="` + html.EscapeString(id) + `" type="application/json">` + string(raw) + `</script>`)
}
func (r *Renderer) assets(entry, nonce string) *pongo2.Value {
	raw, err := os.ReadFile(filepath.Join(r.Root, "static/frontend/.vite/manifest.json"))
	if err != nil {
		return pongo2.AsSafeValue("<!-- Frontend build unavailable -->")
	}
	var manifest map[string]struct {
		File string   `json:"file"`
		CSS  []string `json:"css"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return pongo2.AsSafeValue("")
	}
	chunk, ok := manifest[entry]
	if !ok {
		return pongo2.AsSafeValue("")
	}
	safe := func(path string) bool {
		return !strings.Contains(path, "..") && !strings.ContainsAny(path, "\\?#") && !strings.HasPrefix(path, "/")
	}
	if !safe(chunk.File) {
		return pongo2.AsSafeValue("")
	}
	output := ""
	for _, css := range chunk.CSS {
		if safe(css) {
			output += `<link rel="stylesheet" href="/static/frontend/` + html.EscapeString(css) + `">`
		}
	}
	output += `<script type="module" nonce="` + html.EscapeString(nonce) + `" src="/static/frontend/` + html.EscapeString(chunk.File) + `"></script>`
	return pongo2.AsSafeValue(output)
}

var filterOnce sync.Once

func registerFilters() {
	filterOnce.Do(func() {
		for name, fn := range map[string]pongo2.FilterFunction{
			"safe_href": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				raw := in.String()
				parsed, err := url.Parse(raw)
				if err != nil || parsed.User != nil || strings.ContainsAny(raw, "\r\n\\") || !((strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//")) || parsed.Scheme == "http" || parsed.Scheme == "https") {
					raw = ""
				}
				return pongo2.AsValue(raw), nil
			},
			"cents": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				return pongo2.AsValue(fmt.Sprintf("%.2f", in.Float()/100)), nil
			},
			"avatar_uri": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				if object, ok := in.Interface().(map[string]any); ok {
					return pongo2.AsValue(object["avatar_uri"]), nil
				}
				return pongo2.AsValue(""), nil
			},
			"place_url": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				if object, ok := in.Interface().(map[string]any); ok {
					return pongo2.AsValue(routeURL("place_detail", object["id"])), nil
				}
				return pongo2.AsValue(""), nil
			},
			"event_url": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				if object, ok := in.Interface().(map[string]any); ok {
					return pongo2.AsValue(routeURL("event_detail", object["id"])), nil
				}
				return pongo2.AsValue(""), nil
			},
			"timeuntil": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				date, ok := in.Interface().(time.Time)
				if !ok {
					date, _ = time.Parse(time.RFC3339, in.String())
				}
				minutes := max(0, int(time.Until(date).Minutes()))
				label := fmt.Sprintf("%d minutes", minutes)
				if minutes >= 1440 {
					label = fmt.Sprintf("%d days", minutes/1440)
				} else if minutes >= 60 {
					label = fmt.Sprintf("%d hours", minutes/60)
				}
				return pongo2.AsValue(label), nil
			},
			"date": func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
				date, ok := in.Interface().(time.Time)
				if !ok {
					date, _ = time.Parse(time.RFC3339, in.String())
				}
				return pongo2.AsValue(djangoDate(date, param.String())), nil
			},
		} {
			if pongo2.FilterExists(name) {
				_ = pongo2.ReplaceFilter(name, fn)
			} else {
				_ = pongo2.RegisterFilter(name, fn)
			}
		}
	})
}
func djangoDate(t time.Time, format string) string {
	if t.IsZero() {
		return ""
	}
	if zone, err := time.LoadLocation("Europe/Bucharest"); err == nil {
		t = t.In(zone)
	}
	var b strings.Builder
	escaped := false
	for _, r := range format {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		token, ok := map[rune]string{'D': "Mon", 'l': "Monday", 'd': "02", 'j': "2", 'm': "01", 'M': "Jan", 'F': "January", 'Y': "2006", 'y': "06", 'H': "15", 'i': "04", 's': "05", 'c': time.RFC3339, 'T': "MST"}[r]
		if ok {
			b.WriteString(t.Format(token))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func activitySVG(value any) *pongo2.Value {
	seed, ok := value.(string)
	if !ok {
		row := spaMap(value)
		typ := row["activity_type"]
		slug := spaText(typ)
		if object := spaMap(typ); object["slug"] != nil {
			slug = spaText(object["slug"])
		}
		seed = slug + ":" + spaText(row["title"])
	}
	return pongo2.AsSafeValue(avatars.ActivityAccentSVG(seed))
}

func encodedBlock(message string, values ...any) *pongo2.Value {
	raw, err := base64.RawStdEncoding.DecodeString(message)
	if err != nil {
		return pongo2.AsSafeValue("")
	}
	return translateBlock(string(raw), values...)
}
func tupleLoops(source string) string {
	tags := regexp.MustCompile(`\{%.*?%\}`)
	stack := []bool{}
	return tags.ReplaceAllStringFunc(source, func(tag string) string {
		words := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(tag, "{%"), "%}"))
		if len(words) == 0 {
			return tag
		}
		if words[0] == "for" {
			at := strings.Index(tag, " in ")
			multi := at > 0 && strings.Contains(tag[:at], ",")
			stack = append(stack, multi)
			if multi {
				left := strings.TrimSpace(strings.TrimPrefix(tag[:at], "{% for"))
				names := strings.Split(left, ",")
				pairs := []string{}
				for i, name := range names {
					pairs = append(pairs, strings.TrimSpace(name)+"=native_tuple."+strconv.Itoa(i))
				}
				return "{% for native_tuple" + tag[at:] + "{% with " + strings.Join(pairs, " ") + " %}"
			}
		}
		if words[0] == "empty" && len(stack) > 0 && stack[len(stack)-1] {
			stack[len(stack)-1] = false
			return "{% endwith %}" + tag
		}
		if words[0] == "endfor" && len(stack) > 0 {
			multi := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if multi {
				return "{% endwith %}" + tag
			}
		}
		return tag
	})
}
