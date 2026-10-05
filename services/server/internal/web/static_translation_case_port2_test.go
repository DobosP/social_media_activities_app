package web

import (
	"strings"
	"testing"

	"github.com/flosch/pongo2/v6"
)

func TestWebCasePort2StaticTranslationLiteralsPreserveEntitiesAndEscapeVariables(t *testing.T) {
	set := pongo2.NewSet("static-translation-case", pongo2.DefaultLoader)
	tpl, err := set.FromString(transformTemplate(`{% trans "&#10003; I've arrived &ldquo;here&rdquo;" %}|{% trans "How long your data is kept" %}|{% trans "None is not in this copy" %}|{{ display_name }}|{{ message }}|<input value="{{ attribute }}">|{{ translate(dynamic_message) }}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := tpl.Execute(pongo2.Context{"translate": func(text string) string { return text }, "display_name": `<img src=x onerror=alert(1)>`, "message": `<script>alert(2)</script>`, "attribute": `" autofocus onfocus="alert(3)`, "dynamic_message": `<b onclick="alert(4)">dynamic</b>`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, `&#10003; I've arrived &ldquo;here&rdquo;`) || strings.Contains(got, `&amp;ldquo;`) {
		t.Fatal("trusted static source literals lost entities/apostrophe")
	}
	if !strings.Contains(got, "How long your data is kept") || !strings.Contains(got, "None is not in this copy") {
		t.Fatal("expression rewriting changed a quoted static translation literal")
	}
	for _, unsafe := range []string{`<img`, `<script`, `<b onclick=`, `value="" autofocus`} {
		if strings.Contains(got, unsafe) {
			t.Fatal("dynamic/interpolated value bypassed escaping", unsafe)
		}
	}
	if !strings.Contains(got, `&lt;img`) || !strings.Contains(got, `&lt;script`) || !strings.Contains(got, `&lt;b`) || !strings.Contains(got, `&quot; autofocus`) {
		t.Fatal("negative fixture did not exercise escaped variables/attribute/dynamic translation")
	}
	for _, source := range []string{`{% native_static_translate user_message %}`, `{% native_static_translate "trusted" user_message %}`} {
		if _, err := set.FromString(source); err == nil {
			t.Fatal("static tag accepted a runtime/user expression")
		}
	}
}
