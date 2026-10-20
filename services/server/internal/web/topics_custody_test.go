package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Only complete newest topics images enter live physical readers. Exact
// recovered display predecessors are confined to retained historical controls.
func topicsSourceRelation(path string) (beforeSHA, afterSHA string, beforeBytes, afterBytes int, before, after string, ok bool) {
	switch path {
	case "services/server/internal/web/renderer.go":
		return "98911956e9eb57ac7275da2d27e3888e04b987889e4060d88021976af897eeb6", "24c388bcedad06808589a8c036c2d002a4b2cc87dcebd7f65bbf376ef21fd8a4", 6979, 7158, "\t} else if name == \"web/display_preferences.html\" || name == \"web/access_preferences.html\" {", "\t} else if name == \"web/topic_preferences.html\" {\n\t\traw, renderErr := r.renderTopics(request, defaults)\n\t\tif renderErr != nil {\n\t\t\treturn renderErr\n\t\t}\n\t\t_, _ = output.Write(raw)\n\t} else if name == \"web/display_preferences.html\" || name == \"web/access_preferences.html\" {", true
	case "services/server/internal/web/gui_public_get_walk_test.go":
		return "52140bf078d7c26d1f078e9b15f2869490996b23cca036c6b3a7ca4af9a360dc", "1007f5ef6b4888ab22742138cab1348c514429ff41f2daf76ee359e22cb765a7", 64841, 65310, "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n", "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n\t// Require exact newest topics images before the retained display/history chain.\n\tswitch path {\n\tcase \"services/server/internal/web/renderer.go\", \"services/server/internal/web/gui_public_get_walk_test.go\", \"services/server/internal/web/gui_preferences_layout_test.go\", \"services/server/internal/web/gui_preferences_get_transport_test.go\":\n\t\tvar topicsErr error\n\t\traw, topicsErr = topicsSourcePreimage(path, raw)\n\t\tif topicsErr != nil {\n\t\t\treturn nil, topicsErr\n\t\t}\n\t}\n", true
	case "services/server/internal/web/gui_preferences_layout_test.go":
		return "64026d90ee6963c1dca7b7d2ef1acc27565895beee582e7fca1bdfa27606c4d1", "3e69d9b20ab37392e0787d37fe3ba30df8facb8f831ce72d1d2bd4bde3ce6b2b", 23581, 23575, "\t// Retained notification/display/access bytes use frozen Pongo references;\n\t// ordinary current native authority/markup is checked by App/UI suites.\n\tif err := displayAccessFrozenReferenceRender(renderer, response, request, \"web/\"+fixture.family+\".html\", data); err != nil {", "\t// Retained notification/display/access/topics use frozen Pongo references;\n\t// ordinary current native authority/markup is checked by App/UI suites.\n\tif err := topicsFrozenReferenceRender(renderer, response, request, \"web/\"+fixture.family+\".html\", data); err != nil {", true
	case "services/server/internal/web/gui_preferences_get_transport_test.go":
		return "5b38120ae5a2eb9f2fe89ab5a6a6d3f69673cc34acef3c5f4a41be61dea119ee", "128707ffa6c589ddb43fc1c5336406791df6c07a6815ed30707ec19a961d530b", 28025, 28057, "\tif item.Route == \"notification_preferences\" || item.Route == \"display_preferences\" || item.Route == \"access_preferences\" {\n\t\t// Let the test-only alias retain original template-load trace identities.\n\t\trenderer.loader = trace\n\t}\n\trenderer.set = pongo2.NewSet(\"native-social-current-preference\", trace)\n\ts.Renderer = renderer\n\tmux := http.NewServeMux()\n\ts.Register(mux)\n\trequest := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+item.Path, nil)\n\tif item.Profile == \"adult\" {\n\t\trequest = platform.WithActor(request, actor)\n\t}\n\trequest.AddCookie(&http.Cookie{Name: \"django_language\", Value: \"en\"})\n\trequest.AddCookie(&http.Cookie{Name: \"csrftoken\", Value: strings.Repeat(\"g\", 52)})\n\tif item.State == \"contrast\" {\n\t\tfor name, value := range map[string]string{\"display_theme\": \"contrast\", \"display_text\": \"larger\", \"display_motion\": \"reduce\"} {\n\t\t\trequest.AddCookie(&http.Cookie{Name: name, Value: value})\n\t\t}\n\t}\n\tresponse := httptest.NewRecorder()\n\t*requests = *requests + 1\n\t// Only retained notification/display/access form predicates use references.\n\t// Other routes execute the current registered native mux.\n\tdisplayAccessFrozenReferenceHandler(s, mux).ServeHTTP(response, request)", "\tif item.Route == \"notification_preferences\" || item.Route == \"display_preferences\" || item.Route == \"access_preferences\" || item.Route == \"topic_preferences\" {\n\t\t// Let the test-only alias retain original template-load trace identities.\n\t\trenderer.loader = trace\n\t}\n\trenderer.set = pongo2.NewSet(\"native-social-current-preference\", trace)\n\ts.Renderer = renderer\n\tmux := http.NewServeMux()\n\ts.Register(mux)\n\trequest := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+item.Path, nil)\n\tif item.Profile == \"adult\" {\n\t\trequest = platform.WithActor(request, actor)\n\t}\n\trequest.AddCookie(&http.Cookie{Name: \"django_language\", Value: \"en\"})\n\trequest.AddCookie(&http.Cookie{Name: \"csrftoken\", Value: strings.Repeat(\"g\", 52)})\n\tif item.State == \"contrast\" {\n\t\tfor name, value := range map[string]string{\"display_theme\": \"contrast\", \"display_text\": \"larger\", \"display_motion\": \"reduce\"} {\n\t\t\trequest.AddCookie(&http.Cookie{Name: name, Value: value})\n\t\t}\n\t}\n\tresponse := httptest.NewRecorder()\n\t*requests = *requests + 1\n\t// Only retained notification/display/access/topics predicates use references.\n\t// Other routes execute the current registered native mux.\n\ttopicsFrozenReferenceHandler(s, mux).ServeHTTP(response, request)", true
	}
	return "", "", 0, 0, "", "", false
}
func topicsSourcePreimage(path string, raw []byte) ([]byte, error) {
	prior, completionErr := completionSourcePreimage(path, raw)
	if completionErr != nil {
		return nil, completionErr
	}
	return topicsEpochSourcePreimage(path, prior)
}
func topicsEpochSourcePreimage(path string, raw []byte) ([]byte, error) {
	beforeSHA, afterSHA, beforeBytes, afterBytes, before, after, ok := topicsSourceRelation(path)
	if !ok || len(raw) != afterBytes || guiPublicHash(raw) != afterSHA || bytes.Count(raw, []byte(after)) != 1 {
		return nil, fmt.Errorf("topics-complete-current-source")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(after), []byte(before), 1)
	if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		return nil, fmt.Errorf("topics-exact-source-preimage")
	}
	return previous, nil
}
func topicsPriorSource(path string, raw []byte) ([]byte, error) {
	var completionErr error
	raw, completionErr = completionSourceStage(path, raw)
	if completionErr != nil {
		return nil, completionErr
	}
	beforeSHA, _, beforeBytes, _, _, _, ok := topicsSourceRelation(path)
	if !ok || len(raw) == beforeBytes && guiPublicHash(raw) == beforeSHA {
		return append([]byte(nil), raw...), nil
	}
	return topicsEpochSourcePreimage(path, raw)
}

// Only exact current images and a real unique retained predecessor literal can
// produce a mutation fixture. The original controls keep all their assertions.
func topicsMutationTarget(path string, physical []byte, priorTarget string) (string, error) {
	completionPhysical := physical
	var completionErr error
	physical, completionErr = completionSourceStage(path, physical)
	if completionErr != nil {
		return "", completionErr
	}
	previous, err := topicsEpochSourcePreimage(path, physical)
	if err != nil {
		return "", err
	}
	if priorTarget == "" || bytes.Count(previous, []byte(priorTarget)) != 1 {
		return "", fmt.Errorf("topics-original-mutation-target")
	}
	_, _, _, _, before, after, _ := topicsSourceRelation(path)
	target := priorTarget
	if bytes.Count(physical, []byte(target)) != 1 && before != "" {
		count := bytes.Count([]byte(target), []byte(before))
		if count > 1 {
			return "", fmt.Errorf("topics-ambiguous-mutation-prefix")
		}
		if count == 1 {
			target = string(bytes.Replace([]byte(target), []byte(before), []byte(after), 1))
		}
	}
	if bytes.Count(physical, []byte(target)) != 1 {
		return "", fmt.Errorf("topics-unique-current-mutation-target")
	}
	return completionMutationTarget(path, completionPhysical, target)
}
func TestNativeTopicsCustodyRequiresNewestCompleteSourcesAndRetainsEveryOriginalTuple(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil {
		t.Fatal("original registry unavailable")
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go", "services/server/internal/web/gui_preferences_layout_test.go", "services/server/internal/web/gui_preferences_get_transport_test.go"} {
		physical, binding, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil {
			t.Fatal("physical source unavailable")
		}
		previous, err := topicsSourcePreimage(path, physical)
		beforeSHA, _, beforeBytes, _, _, after, _ := topicsSourceRelation(path)
		after, err = completionMutationTarget(path, physical, after)
		if err != nil {
			t.Fatal("completion physical mutation literal unavailable", err)
		}
		if err != nil || len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
			t.Fatal("complete original predecessor not recovered")
		}
		oldControl, err := topicsPriorSource(path, previous)
		if err != nil || !bytes.Equal(oldControl, previous) {
			t.Fatal("exact retained predecessor refused")
		}
		oldDisplay, oldErr := displayAccessSourcePreimage(path, previous)
		currentDisplay, currentErr := displayAccessSourcePreimage(path, physical)
		if oldErr != nil || currentErr != nil || !bytes.Equal(oldDisplay, currentDisplay) {
			t.Fatal("retained display and earlier source authority changed")
		}
		historical, err := guiPublicGetRowsPreimage(path, physical)
		if err != nil || len(historical) == 0 {
			t.Fatal("entire retained original chain unavailable")
		}
		wanted, exists := registry.Sources[path]
		if exists {
			recovered, err := groupCreatePreferenceSourceBinding(path, physical, binding, wanted)
			if err != nil || !guiPublicGetBindingLive(recovered, wanted) {
				t.Fatal("live preference reader lost original authority")
			}
		} else if path != "services/server/internal/web/gui_preferences_get_transport_test.go" {
			t.Fatal("real original source authority missing")
		}
		for name, changed := range map[string][]byte{
			"previous-is-not-physical": previous,
			"unrelated-LF":             append(append([]byte(nil), physical...), '\n'),
			"truncated":                physical[:len(physical)-1],
			"missing-literal":          bytes.Replace(physical, []byte(after), nil, 1),
			"duplicate-literal":        append(append([]byte(nil), physical...), []byte(after)...),
			"modified-literal":         bytes.Replace(physical, []byte(after), []byte(after+" "), 1),
		} {
			t.Run(filepath.Base(path)+"/"+name, func(t *testing.T) {
				if bytes.Equal(physical, changed) {
					t.Fatal("negative source fixture became a no-op")
				}
				if _, err := topicsSourcePreimage(path, changed); err == nil {
					t.Fatal("old or changed full current source admitted")
				}
				if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
					t.Fatal("ordinary original source reader bypassed current epoch")
				}
				changedBinding := guiG1Binding{len(changed), guiPublicHash(changed), binding.FullMode}
				if exists {
					if _, err := groupCreatePreferenceSourceBinding(path, changed, changedBinding, wanted); err == nil {
						t.Fatal("live reader admitted rehashed physical drift")
					}
				}
				if _, err := topicsMutationTarget(path, changed, "nonexistent-original-target"); err == nil {
					t.Fatal("mutation helper admitted changed physical image")
				}
			})
		}
		if again, err := os.ReadFile(filepath.Join(root, path)); err != nil || !bytes.Equal(again, physical) {
			t.Fatal("source controls changed owning bytes")
		}
	}
	inputs, err := guiPreferenceGETInputs(root, registry)
	if err != nil || len(inputs) != 138 {
		t.Fatal("all138 current source tuples required")
	}
	for path, binding := range inputs {
		_, physical, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding != physical {
			t.Fatal("historical metadata replaced physical current input")
		}
	}
	source, _, sourceErr := guiPublicGetSources(guiPublicGetSourceBytes)
	repairInputs, inputErr := guiPublicGetRepairInputs(root, source)
	if sourceErr != nil || inputErr != nil || len(repairInputs) != 93 || guiPublicGetRepairHistoricalProof(source, repairInputs) != nil {
		t.Fatal("complete original93-input proof unavailable")
	}
}
func TestNativeTopicsMutationBridgeRetainsAllPriorCustodyTargets(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go", "services/server/internal/web/gui_preferences_layout_test.go", "services/server/internal/web/gui_preferences_get_transport_test.go"} {
		physical, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal("physical source unavailable")
		}
		_, _, _, _, _, target, ok := displayAccessSourceRelation(path)
		if !ok {
			t.Fatal("original mutation relation unavailable")
		}
		mapped, err := topicsMutationTarget(path, physical, target)
		if err != nil || bytes.Count(physical, []byte(mapped)) != 1 || bytes.Equal(physical, bytes.Replace(physical, []byte(mapped), nil, 1)) {
			t.Fatal("original mutation target no longer mutates current source")
		}
		if _, err := topicsMutationTarget(path, physical, "nonexistent-original-target"); err == nil {
			t.Fatal("missing original mutation literal admitted")
		}
		_, _, _, _, _, notificationTarget, _ := notificationPreferencesSourceRelation(path)
		if mapped, err := displayAccessMutationTarget(path, physical, notificationTarget); err != nil || bytes.Count(physical, []byte(mapped)) != 1 {
			t.Fatal("retained notification/display mutation lost its real current target")
		}
		if path == "services/server/internal/web/renderer.go" || path == "services/server/internal/web/gui_public_get_walk_test.go" {
			_, _, _, _, _, accountTarget, _ := accountCenterSourceRelation(path)
			if mapped, err := languageActionAccountMutationTarget(path, physical, accountTarget); err != nil || bytes.Count(physical, []byte(mapped)) != 1 {
				t.Fatal("retained account mutation bridge lost real target")
			}
			_, _, _, _, _, inboxTarget, _ := notificationInboxSourceRelation(path)
			if mapped, err := accountCenterInboxMutationTarget(path, physical, inboxTarget); err != nil || bytes.Count(physical, []byte(mapped)) != 1 {
				t.Fatal("retained inbox mutation bridge lost real target")
			}
		}
	}
}
