package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// One new exact inverse precedes the unchanged32-row relation. Only retained
// Group controls may explicitly consume that recovered prior source epoch;
// current live readers call the strict inverse and keep their physical tuple.
func notificationPreferencesSourceRelation(path string) (beforeSHA, afterSHA string, beforeBytes, afterBytes int, before, after string, ok bool) {
	switch path {
	case "services/server/internal/web/renderer.go":
		return "3ba5b4456312437e8edf058b4f9967385bf14e483a0bb6d771b5881231d5c0e6", "367b05a8a05828b75181ff2d1cc4a2164f936758342a3b53dee777496c3314ba", 6146, 6349, "\t} else if name == \"web/group_detail.html\" {", "\t} else if name == \"web/notification_preferences.html\" {\n\t\traw, renderErr := r.renderNotificationPreferences(request, defaults)\n\t\tif renderErr != nil {\n\t\t\treturn renderErr\n\t\t}\n\t\t_, _ = output.Write(raw)\n\t} else if name == \"web/group_detail.html\" {", true
	case "services/server/internal/web/gui_public_get_walk_test.go":
		return "8604ff0a20a1d2eb3ecff08ba5245124c7fccce7b9c223910db03cdef378f096", "ba643be27641c54da5c7f261519ef5ab83203b7e7f4287974db430d793f3f428", 62298, 62819, "", "\t// Exact new notification/source-control inverses precede the unchanged32-row\n\t// Group/family/historical chain. Live tuples stay physical.\n\tswitch path {\n\tcase \"services/server/internal/web/renderer.go\", \"services/server/internal/web/gui_preferences_layout_test.go\", \"services/server/internal/web/gui_preferences_get_transport_test.go\", \"services/server/internal/web/gui_public_get_walk_test.go\":\n\t\tvar err error\n\t\traw, err = notificationPreferencesSourcePreimage(path, raw)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t}\n", true
	case "services/server/internal/web/gui_preferences_layout_test.go":
		return "48f619e8da05af530a391ff5e7c440a2a806d4eb08381ded993b296945ab247c", "4996b5cc5b068abd8ef37a3d953647a4c7f325e9fc7c0bd4c3bec7cf46bddb5c", 23402, 23595, "\tif err := renderer.Render(response, request, \"web/\"+fixture.family+\".html\", data); err != nil {", "\t// Retained notification bytes below are the frozen Pongo reference only;\n\t// current native22-row authority/markup is checked by the new App/UI suites.\n\tif err := notificationPreferencesFrozenReferenceRender(renderer, response, request, \"web/\"+fixture.family+\".html\", data); err != nil {", true
	case "services/server/internal/web/gui_preferences_get_transport_test.go":
		return "290136500c36cb62249ae81c6d9e9f74697ef85074c009d4021bc8f8bac99938", "7f9f6da35a3de5ab374025d18e164c03c2c7ec9006bb19febd5ae49a9164f350", 27617, 27970, "\trenderer.set = pongo2.NewSet(\"native-social-current-preference\", trace)\n\ts.Renderer = renderer\n\tmux := http.NewServeMux()\n\ts.Register(mux)\n\trequest := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+item.Path, nil)\n\tif item.Profile == \"adult\" {\n\t\trequest = platform.WithActor(request, actor)\n\t}\n\trequest.AddCookie(&http.Cookie{Name: \"django_language\", Value: \"en\"})\n\trequest.AddCookie(&http.Cookie{Name: \"csrftoken\", Value: strings.Repeat(\"g\", 52)})\n\tif item.State == \"contrast\" {\n\t\tfor name, value := range map[string]string{\"display_theme\": \"contrast\", \"display_text\": \"larger\", \"display_motion\": \"reduce\"} {\n\t\t\trequest.AddCookie(&http.Cookie{Name: name, Value: value})\n\t\t}\n\t}\n\tresponse := httptest.NewRecorder()\n\t*requests = *requests + 1\n\tmux.ServeHTTP(response, request)\n", "\tif item.Route == \"notification_preferences\" {\n\t\t// Let the test-only alias retain original template-load trace identities.\n\t\trenderer.loader = trace\n\t}\n\trenderer.set = pongo2.NewSet(\"native-social-current-preference\", trace)\n\ts.Renderer = renderer\n\tmux := http.NewServeMux()\n\ts.Register(mux)\n\trequest := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+item.Path, nil)\n\tif item.Profile == \"adult\" {\n\t\trequest = platform.WithActor(request, actor)\n\t}\n\trequest.AddCookie(&http.Cookie{Name: \"django_language\", Value: \"en\"})\n\trequest.AddCookie(&http.Cookie{Name: \"csrftoken\", Value: strings.Repeat(\"g\", 52)})\n\tif item.State == \"contrast\" {\n\t\tfor name, value := range map[string]string{\"display_theme\": \"contrast\", \"display_text\": \"larger\", \"display_motion\": \"reduce\"} {\n\t\t\trequest.AddCookie(&http.Cookie{Name: name, Value: value})\n\t\t}\n\t}\n\tresponse := httptest.NewRecorder()\n\t*requests = *requests + 1\n\t// Only retained notification form-byte predicates use the frozen reference.\n\t// Every other route still executes its current registered native mux.\n\tnotificationPreferencesFrozenReferenceHandler(s, mux).ServeHTTP(response, request)\n", true
	}
	return "", "", 0, 0, "", "", false
}
func notificationPreferencesSourcePreimage(path string, raw []byte) ([]byte, error) {
	if path == "services/server/internal/web/gui_preferences_layout_test.go" || path == "services/server/internal/web/gui_preferences_get_transport_test.go" {
		var displayErr error
		raw, displayErr = displayAccessPriorSource(path, raw)
		if displayErr != nil {
			return nil, displayErr
		}
	}
	// Retained prior controls may enter on the exact recovered inbox preimage.
	var inboxErr error
	raw, inboxErr = notificationInboxPriorSource(path, raw)
	if inboxErr != nil {
		return nil, inboxErr
	}
	beforeSHA, afterSHA, beforeBytes, afterBytes, before, after, ok := notificationPreferencesSourceRelation(path)
	if !ok || len(raw) != afterBytes || guiPublicHash(raw) != afterSHA || bytes.Count(raw, []byte(after)) != 1 {
		return nil, fmt.Errorf("notification-complete-current-source")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(after), []byte(before), 1)
	if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		return nil, fmt.Errorf("notification-exact-source-preimage")
	}
	return previous, nil
}
func notificationPreferencesRendererPreimage(raw []byte) ([]byte, error) {
	return notificationPreferencesSourcePreimage("services/server/internal/web/renderer.go", raw)
}
func notificationPreferencesWalkerPreimage(raw []byte) ([]byte, error) {
	return notificationPreferencesSourcePreimage("services/server/internal/web/gui_public_get_walk_test.go", raw)
}
func notificationPreferencesPriorSource(path string, raw []byte) ([]byte, error) {
	beforeSHA, _, beforeBytes, _, _, _, ok := notificationPreferencesSourceRelation(path)
	if ok && len(raw) == beforeBytes && guiPublicHash(raw) == beforeSHA {
		return append([]byte(nil), raw...), nil
	}
	return notificationPreferencesSourcePreimage(path, raw)
}

func TestNativeNotificationPreferencesCustodyKeepsCompletePriorChainsAndPhysicalInputs(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	rows := guiPublicGetRepairRows()
	if len(rows) != 32 || len(groupDetailPreviousRepairRows(rows)) != 31 || len(groupCreatePreviousRepairRows()) != 27 {
		t.Fatal("prior32/31/27relations changed")
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go", "services/server/internal/web/gui_preferences_layout_test.go", "services/server/internal/web/gui_preferences_get_transport_test.go"} {
		physical, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		previous, err := notificationPreferencesSourcePreimage(path, physical)
		if err != nil {
			t.Fatal("exact new inverse refused", path, err)
		}
		if _, err = notificationPreferencesSourcePreimage(path, previous); err == nil {
			t.Fatal("former source admitted as current", path)
		}
		for _, changed := range [][]byte{append(append([]byte(nil), physical...), '\n'), physical[:len(physical)-1], previous} {
			if _, err := notificationPreferencesSourcePreimage(path, changed); err == nil {
				t.Fatal("rehashed drift/mixed epoch admitted", path)
			}
		}
		_, _, _, _, _, after, _ := notificationPreferencesSourceRelation(path)
		after, err = displayAccessMutationTarget(path, physical, after)
		if err != nil {
			t.Fatal("latest display/access preference mutation target unavailable")
		}
		for _, changed := range [][]byte{bytes.Replace(physical, []byte(after), nil, 1), append(append([]byte(nil), physical...), []byte(after)...)} {
			if _, err := notificationPreferencesSourcePreimage(path, changed); err == nil {
				t.Fatal("missing/duplicate literal relation admitted", path)
			}
		}
		if path == "services/server/internal/web/renderer.go" {
			historical, err := guiPublicGetRowsPreimage(path, physical)
			if err != nil || len(historical) != 5510 || guiPublicHash(historical) != "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68" {
				t.Fatal("new and all32prior inverses did not recover complete original renderer", err)
			}
		}
	}
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil {
		t.Fatal("immutable original registry unavailable")
	}
	inputs, err := guiPreferenceGETInputs(root, registry)
	if err != nil || len(inputs) != 138 {
		t.Fatal("all138current physical tuples required", err)
	}
	for path, binding := range inputs {
		_, physical, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding != physical {
			t.Fatal("prior metadata leaked into current reader", path, err)
		}
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go"} {
		raw, physical, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil {
			t.Fatal(err)
		}
		wanted := registry.Sources[path]
		former, err := notificationPreferencesSourcePreimage(path, raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, changed := range [][]byte{append(append([]byte(nil), raw...), '\n'), former} {
			binding := guiG1Binding{len(changed), guiPublicHash(changed), physical.FullMode}
			if _, err := groupCreatePreferenceSourceBinding(path, changed, binding, wanted); err == nil {
				t.Fatal("live reader admitted rehashed or former physical source", path)
			}
		}
	}
}
