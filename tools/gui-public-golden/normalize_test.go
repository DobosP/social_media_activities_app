package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func observed(t *testing.T, input string) result {
	t.Helper()
	var output bytes.Buffer
	if err := normalize(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	var got result
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != 1 || got.Policy != policy || len(got.Normalized) == 0 || len(got.Hard) != 0 {
		t.Fatal("bridge lost the released canonical bytes or independent hard findings")
	}
	return got
}

func TestGUIPublicNormalizerPermitsReleasedMasksOnly(t *testing.T) {
	left := `<main><script nonce="first" type="application/json">{"b":2,"csrf":"one"}</script><p title="unchanged">nonce first</p></main>`
	right := `<main><script type="application/json" nonce="second">{"csrf":"two","b":2}</script><p title="unchanged">nonce first</p></main>`
	want := observed(t, left)
	if !bytes.Equal(want.Normalized, observed(t, right).Normalized) {
		t.Fatal("released nonce attribute/JSON-CSRF masks or attribute order were lost")
	}
	for name, changed := range map[string]string{
		"nonce-like-text": strings.Replace(right, "nonce first", "nonce second", 1),
		"ordinary-attribute": strings.Replace(right, `title="unchanged"`, `title="changed"`, 1),
		"non-csrf-json": strings.Replace(right, `"b":2`, `"b":3`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(want.Normalized, observed(t, changed).Normalized) {
				t.Fatal("visible or non-CSRF data was masked by the bridge")
			}
		})
	}
}

func TestGUIPublicNormalizerPreservesTextAttributesAndOrder(t *testing.T) {
	base := `<main><p class="notice" aria-live="polite">Public warning</p><a href="/privacy/">Privacy</a><pre>a  b` + "\n" + `</pre><span>A</span> <span>B</span></main>`
	want := observed(t, base)
	mutations := map[string]string{
		"one-character": strings.Replace(base, "Public warning", "Public warninh", 1),
		"drop-warning-arm": strings.Replace(base, `<p class="notice" aria-live="polite">Public warning</p>`, "", 1),
		"drop-attribute": strings.Replace(base, ` aria-live="polite"`, "", 1),
		"change-url": strings.Replace(base, `href="/privacy/"`, `href="/terms/"`, 1),
		"sibling-order": strings.Replace(base, `<p class="notice" aria-live="polite">Public warning</p><a href="/privacy/">Privacy</a>`, `<a href="/privacy/">Privacy</a><p class="notice" aria-live="polite">Public warning</p>`, 1),
		"preformatted-space": strings.Replace(base, "a  b", "a b", 1),
		"inline-gap-presence": strings.Replace(base, "</span> <span>", "</span><span>", 1),
	}
	for name, changed := range mutations {
		t.Run(name, func(t *testing.T) {
			if changed == base || bytes.Equal(want.Normalized, observed(t, changed).Normalized) {
				t.Fatal("the meaningful mutation disappeared")
			}
		})
	}
}

func TestGUIPublicNormalizerRetainsHardRefusalsIndependently(t *testing.T) {
	for name, input := range map[string]string{
		"empty-href": `<a href="">Privacy</a>`,
		"empty-src": `<img src="">`,
		"empty-action": `<form action=""></form>`,
		"failed-url-sanitization": `<a href="about:invalid#TemplFailedSanitizationURL">Privacy</a>`,
		"template-variable-leak": `<p>{{ person }}</p>`,
		"template-tag-leak": `<p>{% if allowed %}</p>`,
		"invalid-json": `<script type="application/json">{broken}</script>`,
	} {
		t.Run(name, func(t *testing.T) {
			var first, second bytes.Buffer
			if normalize(strings.NewReader(input), &first) == nil || normalize(strings.NewReader(input), &second) == nil {
				t.Fatal("hard finding became success")
			}
			var got result
			if json.Unmarshal(first.Bytes(), &got) != nil || len(got.Hard) == 0 || len(got.Normalized) == 0 {
				t.Fatal("hard-finding output was lost")
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatal("control inputs unexpectedly differed")
			}
			// Identical canonical output still refuses: equality cannot erase hard findings.
		})
	}
}

func TestGUIPublicNormalizerBoundsRawInput(t *testing.T) {
	for _, raw := range [][]byte{nil, bytes.Repeat([]byte("x"), (512<<10)+1)} {
		var output bytes.Buffer
		if normalize(bytes.NewReader(raw), &output) == nil || output.Len() != 0 {
			t.Fatal("empty or oversized input emitted accepting output")
		}
	}
}
