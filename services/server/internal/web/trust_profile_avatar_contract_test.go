package web

import (
	"encoding/base64"
	"html"
	"strings"
	"testing"
)

func TestNativeTrustProfileGeneratedAvatarKeepsExactCurrentHTMLContract(t *testing.T) {
	// Six tildes guarantee at least one '+' in the standard base64 alphabet.
	// A generic html/template URL value serializes it as &#43;, unlike the
	// established raw profile response contract checked by authority tests.
	avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>~~~~~~</text></svg>`))
	if !strings.Contains(avatar, "+") {
		t.Fatal("avatar regression fixture must exercise literal plus")
	}
	for _, name := range []string{"web/person.html", "web/_person_card.html"} {
		for _, tier := range []string{"stranger", "shared", "connected"} {
			t.Run(name+"/"+tier, func(t *testing.T) {
				r, request, data := trustFixture(t, name)
				card := data["card"].(map[string]any)
				card["avatar"], card["tier"] = avatar, tier
				card["display"] = `Profile </strong><script>display</script> & "label"`
				card["show_photo"] = false
				page, err := r.trustPage(request, name, data)
				if err != nil {
					t.Fatal(err)
				}
				if string(page.Person.Avatar) != avatar || string(page.Person.AvatarSource) != `src="`+avatar+`"` {
					t.Fatal("validated generated URI changed in the typed profile")
				}
				raw, err := r.renderTrust(request, name, data)
				if err != nil {
					t.Fatal(err)
				}
				body := string(raw)
				if !strings.Contains(body, `src="`+avatar+`"`) {
					t.Fatal("profile response lost its exact generated avatar URI")
				}
				if strings.Contains(body, "<script>display</script>") || !strings.Contains(body, html.EscapeString(card["display"].(string))) {
					t.Fatal("profile display stopped being escaped")
				}
				if tier == "stranger" && (strings.Contains(body, "generated-peer") || strings.Contains(body, "Quiet") || page.Person.CanConnect || page.Person.CanMessage) {
					t.Fatal("literal avatar presentation broadened the minimal card")
				}
			})
		}
	}
}

func TestNativeTrustProfileLiteralAvatarKeepsExistingImageOnlyBoundary(t *testing.T) {
	for _, value := range []string{
		"javascript:alert(1)",
		"https://untrusted.example/avatar.svg",
		"/api/media/file/unrelated/",
		"data:text/html;base64,PHNjcmlwdD4=",
		`data:image/svg+xml;base64,broken" onerror="alert(1)`,
		"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<html>not SVG</html>`)),
	} {
		r, request, data := trustFixture(t, "web/_person_card.html")
		data["card"].(map[string]any)["avatar"] = value
		raw, err := r.renderTrust(request, "web/_person_card.html", data)
		if err == nil || len(raw) != 0 {
			t.Fatal("unsafe/non-generated image URI rendered or wrote partial output")
		}
	}
}
