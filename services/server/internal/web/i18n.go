package web

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"github.com/flosch/pongo2/v6"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type translation struct {
	Plural string
	Text   map[int]string
	Fuzzy  bool
}
type translationCatalog map[string]translation

func loadCatalog(root string) translationCatalog {
	file, err := os.Open(filepath.Join(root, "locale/ro/LC_MESSAGES/django.po"))
	if err != nil {
		return translationCatalog{}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 2<<20 {
		return translationCatalog{}
	}
	result, _ := parseCatalog(file) // Preserve the original disk path's partial-catalog behavior.
	return result
}

func parseCatalog(reader io.Reader) (translationCatalog, error) {
	result := translationCatalog{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 256<<10)
	key := ""
	entry := translation{Text: map[int]string{}}
	part := ""
	index := 0
	flush := func() {
		if key != "" && !entry.Fuzzy {
			result[key] = entry
			if entry.Plural != "" {
				result[entry.Plural] = entry
			}
		}
		key = ""
		entry = translation{Text: map[int]string{}}
		part = ""
		index = 0
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "#, ") && strings.Contains(line, "fuzzy") {
			entry.Fuzzy = true
		}
		if strings.HasPrefix(line, "msgid ") {
			part = "id"
			line = strings.TrimPrefix(line, "msgid ")
		} else if strings.HasPrefix(line, "msgid_plural ") {
			part = "plural"
			line = strings.TrimPrefix(line, "msgid_plural ")
		} else if strings.HasPrefix(line, "msgstr[") {
			end := strings.Index(line, "]")
			if end < 0 {
				continue
			}
			index, _ = strconv.Atoi(line[7:end])
			part = "text"
			line = strings.TrimSpace(line[end+1:])
		} else if strings.HasPrefix(line, "msgstr ") {
			part = "text"
			index = 0
			line = strings.TrimPrefix(line, "msgstr ")
		} else if !strings.HasPrefix(line, "\"") {
			continue
		}
		value, err := strconv.Unquote(line)
		if err != nil {
			continue
		}
		switch part {
		case "id":
			key += value
		case "plural":
			entry.Plural += value
		case "text":
			entry.Text[index] += value
		}
	}
	flush()
	return result, scanner.Err()
}
func (c translationCatalog) translate(language, message string, count int) string {
	if language != "ro" {
		return message
	}
	entry, ok := c[message]
	if !ok {
		return message
	}
	index := 0
	if entry.Plural != "" {
		if count == 1 {
			index = 0
		} else if count%100 > 19 || count%100 == 0 && count != 0 {
			index = 2
		} else {
			index = 1
		}
	}
	if translated := entry.Text[index]; translated != "" {
		return translated
	}
	return message
}
func language(r *http.Request) string {
	if cookie, err := r.Cookie("django_language"); err == nil && (cookie.Value == "ro" || cookie.Value == "en") {
		return cookie.Value
	}
	for _, raw := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		lang := strings.TrimSpace(strings.Split(raw, ";")[0])
		if strings.HasPrefix(lang, "ro") {
			return "ro"
		}
		if strings.HasPrefix(lang, "en") {
			return "en"
		}
	}
	return "en"
}

var namedPlaceholder = regexp.MustCompile(`%\([^)]+\)s|%s`)

func translatedBlock(c translationCatalog, lang, encoded string, count int, values ...any) *pongo2.Value {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return pongo2.AsSafeValue("")
	}
	source := string(raw)
	arguments := map[string]string{}
	for i, placeholder := range namedPlaceholder.FindAllString(source, -1) {
		if i < len(values) {
			arguments[placeholder] = html.EscapeString(fmt.Sprint(values[i]))
		}
	}
	message := c.translate(lang, source, count)
	message = namedPlaceholder.ReplaceAllStringFunc(message, func(match string) string {
		if value, ok := arguments[match]; ok {
			return value
		}
		return match
	})
	return pongo2.AsSafeValue(message)
}
func display(r *http.Request, name, defaultValue string, allowed ...string) string {
	if cookie, err := r.Cookie(name); err == nil {
		for _, value := range allowed {
			if cookie.Value == value {
				return value
			}
		}
	}
	return defaultValue
}
