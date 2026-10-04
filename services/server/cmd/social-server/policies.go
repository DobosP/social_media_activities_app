package main

// These source settings govern hard-coded native policy. Until a native policy
// seam exists, a nondefault override must stop startup rather than change a
// deployment's safety/publication meaning without effect.
func validateFixedPolicies(d *decoder) {
	for _, name := range []string{"ASGI_THREADS", "DJANGO_SETTINGS_MODULE"} {
		if d.isSet(name) {
			d.invalid(name)
		}
	}
	for _, setting := range []struct {
		name  string
		value bool
	}{{"CHILD_PUBLIC_VENUES_ONLY", true}, {"PROGRESSION_AVATAR_PUBLIC", false}, {"MEDIA_REQUIRE_SCANNER", true}} {
		d.fixedBool(setting.name, setting.value)
	}
	for _, setting := range []struct{ name, value string }{
		{"CHAT_MESSAGE_POLICY", "apps.chat.policy.NudgeMessagePolicy"}, {"THREAD_REACTION_FACETS", ""},
	} {
		d.fixedString(setting.name, setting.value)
	}
	d.fixedString("REDIS_URL", "")
	switch d.get("MEDIA_S3_SSE") {
	case "", "AES256", "aws:kms", "aws:kms:dsse":
	default:
		d.invalid("MEDIA_S3_SSE")
	}
	switch d.value("MEDIA_S3_ADDRESSING_STYLE", "auto") {
	case "auto", "path", "virtual":
	default:
		d.invalid("MEDIA_S3_ADDRESSING_STYLE")
	}
	extra := d.stringMap("INGESTION_EXTRA_ADAPTERS")
	for slug, path := range extra {
		if slug != "roedu" || (path != "roedu" && path != "apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter") {
			d.invalid("INGESTION_EXTRA_ADAPTERS")
		}
	}
	// Postgres backs native durable fanout. A custom Python Channels layer or
	// third-party source/provider needs an explicit native adapter.
	layer := d.value("CHANNEL_LAYER_BACKEND", "channels.layers.InMemoryChannelLayer")
	if layer != "channels.layers.InMemoryChannelLayer" && layer != "postgres" {
		d.invalid("CHANNEL_LAYER_BACKEND")
	}
	d.fixedString("ROEDU_APP_PACK", "roedu:social_media_activities_app:events_places:v1")
}
