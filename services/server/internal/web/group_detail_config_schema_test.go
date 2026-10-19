package web

import "testing"

func TestNativeGroupDetailLiveLabelsRequireEveryCurrentKeyAndStringType(t *testing.T) {
	for _, mode := range []string{"missing-plus-extra", "present-null", "non-string"} {
		t.Run(mode, func(t *testing.T) {
			r, request, data := groupDetailProjectionFixture(t)
			config := data["thread_chat_config"].(map[string]any)
			labels := config["i18n"].(map[string]string)
			if mode == "missing-plus-extra" {
				delete(labels, "reply")
				labels["unknown-current-label"] = "Synthetic extra label"
			} else {
				// The real producer is map[string]string. A decoded null or another
				// value type must refuse, never turn a missing label into empty copy.
				changed := map[string]any{}
				for key, value := range labels {
					changed[key] = value
				}
				if mode == "present-null" {
					changed["reply"] = nil
				} else {
					changed["reply"] = 7
				}
				config["i18n"] = changed
			}
			body, err := r.renderGroupDetail(request, data)
			if err == nil || len(body) != 0 {
				t.Fatal("partial/unknown/null/nonstring live label schema produced output", mode, err)
			}
		})
	}
}
