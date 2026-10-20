package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/flosch/pongo2/v6"
)

func TestNativePublicPlaceDecorativeImageKeepsValidatedSVGBytes(t *testing.T) {
	for _, language := range []string{"en", "ro"} {
		t.Run(language, func(t *testing.T) {
			r, request, data := publicPlaceNativeFixture(t)
			data["language"] = language
			before, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			p, err := r.publicPlaceProjection(request, data)
			if err != nil {
				t.Fatal(err)
			}
			const prefix = "data:image/svg+xml;base64,"
			url := string(p.Visual.AccentURL)
			if !strings.HasPrefix(url, prefix) || p.Visual.URL != "" || p.Visual.Alt != "" {
				t.Fatal("validated decorative image lost its fixed MIME boundary or photo separation")
			}
			decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(url, prefix))
			expected := activitySVG("place:generated:Generated <venue> & location").String()
			if err != nil || string(decoded) != expected {
				t.Fatal("decorative image changed the validated producer SVG bytes")
			}
			body := html.UnescapeString(publicPlaceNativeRender(t, r, request, data))
			image := `<div class="place-cover-accent" aria-hidden="true"><img src="` + url + `" alt="" width="320" height="120"></div>`
			if strings.Count(body, image) != 1 || strings.Contains(body, `<linearGradient id="acc`) {
				t.Fatal("accent was not isolated in one decorative image document")
			}
			after, err := json.Marshal(data)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("decorative image projection mutated the selected producer data")
			}
		})
	}
	for _, supplied := range []any{
		nil,
		activitySVG("place:generated:Generated <venue> & location").String(),
		pongo2.AsSafeValue(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`),
		pongo2.AsSafeValue(activitySVG("place:generated:Generated <venue> & location").String() + `<script>alert(1)</script>`),
	} {
		r, request, data := publicPlaceNativeFixture(t)
		data["place_visual"].(map[string]any)["svg"] = supplied
		if _, err := r.publicPlaceProjection(request, data); err == nil {
			t.Fatal("unvalidated SVG entered the trusted decorative-image URL")
		}
	}
}
