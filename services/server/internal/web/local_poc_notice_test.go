package web

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestLocalPOCNoticeUsesProducerFlagsAcrossNativeAndCompatibilityPages(t *testing.T) {
	for _, name := range []string{"web/topic_preferences.html", "web/privacy.html"} {
		for _, flag := range []string{"local_poc_mode", "local_poc_content"} {
			r, request, data := topicsNativeFixture(t)
			before := httptest.NewRecorder()
			if err := r.Render(before, request, name, data); err != nil {
				t.Fatal("ordinary page render failed", err)
			}
			if bytes.Contains(before.Body.Bytes(), []byte(localPOCNotice)) {
				t.Fatal("ordinary page acquired a development disclosure")
			}
			data[flag] = true
			after := httptest.NewRecorder()
			if err := r.Render(after, request, name, data); err != nil {
				t.Fatal("producer-selected development page failed", err)
			}
			if bytes.Count(after.Body.Bytes(), []byte(localPOCNotice)) != 1 || !bytes.Equal(bytes.Replace(after.Body.Bytes(), []byte(localPOCNotice), nil, 1), before.Body.Bytes()) {
				t.Fatal("producer flag must add only the existing development notice")
			}
			if after.Header().Get("Cache-Control") != before.Header().Get("Cache-Control") || after.Header().Get("Content-Type") != before.Header().Get("Content-Type") || after.Header().Get("Vary") != before.Header().Get("Vary") {
				t.Fatal("development disclosure changed response policy")
			}
		}
	}
}

func TestLocalPOCNoticePreservesDefaultBodiesAndExistingDisclosure(t *testing.T) {
	plain := []byte(`<html><main id="main"><h1>Content</h1></main></html>`)
	got, err := preserveLocalPOCNotice(plain, false)
	if err != nil || !bytes.Equal(got, plain) || &got[0] != &plain[0] {
		t.Fatal("unselected development mode changed the existing body")
	}
	existing := []byte(`<main id="main">` + localPOCNotice + `<h1>Content</h1></main>`)
	got, err = preserveLocalPOCNotice(existing, true)
	if err != nil || !bytes.Equal(got, existing) {
		t.Fatal("compatibility renderer disclosure must stay byte-exact")
	}
}

func TestLocalPOCNoticeLabelsNativeFullPagesOnceWithoutChangingContent(t *testing.T) {
	for _, opening := range []string{`<main>`, `<main class="family-main" id="main" tabindex="-1">`, "<main\n id=\"main\">"} {
		before := []byte(`<header>Navigation</header>` + opening + `<h1>Supplied content</h1></main><footer>Links</footer>`)
		got, err := preserveLocalPOCNotice(before, true)
		if err != nil || bytes.Count(got, []byte(localPOCNotice)) != 1 || !bytes.Equal(bytes.Replace(got, []byte(localPOCNotice), nil, 1), before) {
			t.Fatal("native development notice changed page content")
		}
		again, err := preserveLocalPOCNotice(got, true)
		if err != nil || !bytes.Equal(again, got) {
			t.Fatal("native development notice is not idempotent")
		}
	}
}

func TestLocalPOCNoticeLeavesFragmentsAndRefusesDuplicateDisclosure(t *testing.T) {
	fragment := []byte(`<article>Supplied fragment</article>`)
	got, err := preserveLocalPOCNotice(fragment, true)
	if err != nil || !bytes.Equal(got, fragment) {
		t.Fatal("fragment gained a full-page disclosure")
	}
	got, err = preserveLocalPOCNotice([]byte(`<main>`+localPOCNotice+localPOCNotice+`</main>`), true)
	if err == nil || len(got) != 0 {
		t.Fatal("duplicate disclosure must fail before output")
	}
}
