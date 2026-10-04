package main

// These source settings govern hard-coded native policy. Until a native policy
// seam exists, a nondefault override must stop startup rather than change a
// deployment's safety/publication meaning without effect.
func validateFixedPolicies(d *decoder) {
	integers := []struct {
		name  string
		value int
	}{
		{"CLOSURE_REPORT_THRESHOLD", 3}, {"CLOSURE_REPORT_DECAY_SECONDS", 14 * 24 * 3600}, {"CLOSURE_REPORT_RATE_LIMIT", 10}, {"CLOSURE_REPORT_RATE_WINDOW_SECONDS", 3600},
		{"OPEN_NOW_REPORT_THRESHOLD", 3}, {"OPEN_NOW_REPORT_DECAY_SECONDS", 14 * 24 * 3600}, {"OPEN_NOW_REPORT_RATE_LIMIT", 10}, {"OPEN_NOW_REPORT_RATE_WINDOW_SECONDS", 3600},
		{"FACT_QUORUM", 3}, {"FACT_VOTE_RATE_LIMIT", 40}, {"FACT_VOTE_RATE_WINDOW_SECONDS", 3600}, {"CORRECTION_QUORUM", 3}, {"EDGE_QUORUM", 3},
		{"EVENT_REPORT_THRESHOLD", 3}, {"EVENT_REPORT_DECAY_SECONDS", 14 * 24 * 3600}, {"EVENT_REPORT_RATE_LIMIT", 10}, {"EVENT_REPORT_RATE_WINDOW_SECONDS", 3600},
		{"SERIES_SPAWN_LEAD_DAYS", 14}, {"SERIES_SPAWN_BATCH", 500},
		{"SAVED_SEARCH_RATE_LIMIT", 20}, {"SAVED_SEARCH_RATE_WINDOW_SECONDS", 3600}, {"SAVED_SEARCH_MAX_PER_USER", 20}, {"SAVED_SEARCH_MATCH_BATCH", 1000}, {"SAVED_SEARCH_NOTIFY_RATE_LIMIT", 50}, {"SAVED_SEARCH_NOTIFY_WINDOW_SECONDS", 86400},
		{"GUARDIAN_INVITE_RATE_LIMIT", 20}, {"GUARDIAN_INVITE_RATE_WINDOW_SECONDS", 3600}, {"GUARDIAN_GUARDRAIL_RATE_LIMIT", 30}, {"GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS", 3600},
		{"THREAD_POST_RATE_LIMIT", 30}, {"THREAD_POST_RATE_WINDOW_SECONDS", 60}, {"THREAD_REACT_RATE_LIMIT", 60}, {"THREAD_REACT_RATE_WINDOW_SECONDS", 60}, {"SOCIAL_THREAD_POST_LIMIT", 100}, {"SOCIAL_MEMBERSHIP_LIST_LIMIT", 100},
		{"UNSAFE_REPORT_RATE_LIMIT", 12}, {"UNSAFE_REPORT_RATE_WINDOW_SECONDS", 3600}, {"UNSAFE_REPORT_COOLDOWN_SECONDS", 300},
		{"CONNECTIONS_REQUEST_RATE_LIMIT", 20}, {"CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS", 3600},
		{"COMMUNITY_ACTIVITIES_PAGE_SIZE", 100}, {"GROUP_CREATE_RATE_LIMIT", 5}, {"GROUP_CREATE_RATE_WINDOW_SECONDS", 3600}, {"GROUP_JOIN_RATE_LIMIT", 20}, {"GROUP_JOIN_RATE_WINDOW_SECONDS", 3600}, {"GROUP_QUESTION_RATE_LIMIT", 6}, {"GROUP_QUESTION_RATE_WINDOW_SECONDS", 3600},
		{"MESSAGING_RATE_WINDOW_SECONDS", 60}, {"MESSAGING_START_RATE_LIMIT", 20}, {"MESSAGING_SEND_RATE_LIMIT", 60}, {"MESSAGING_MAX_CIPHERTEXT_BYTES", 65536}, {"MESSAGING_MAX_GROUP_MEMBERS", 256},
		{"ARRIVAL_WINDOW_BEFORE_HOURS", 2}, {"ARRIVAL_WINDOW_AFTER_HOURS", 3}, {"DEPARTURE_WINDOW_AFTER_HOURS", 3}, {"ARRIVAL_RETENTION_HOURS", 6}, {"INTEREST_LIFETIME_DAYS", 14}, {"INTEREST_THRESHOLD", 3}, {"PLACE_PROPOSAL_DEDUP_RADIUS_M", 60},
		{"MAX_REQUEST_BODY_BYTES", 8 << 20}, {"DATA_UPLOAD_MAX_MEMORY_SIZE", 8 << 20}, {"CHAT_MAX_LENGTH", 4000},
		{"MEDIA_IMAGE_QUALITY", 0}, {"MEDIA_SIGNED_URL_TTL", 300}, {"MEDIA_PRESIGNED_TTL", 60}, {"MEDIA_ATTACHMENT_MAX_BYTES", 7 << 20}, {"MEDIA_EPHEMERAL_MIN_TTL_SECONDS", 3600}, {"MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS", 86400},
		{"MEDIA_VIDEO_CRF", 23}, {"MEDIA_VIDEO_MAX_ATTEMPTS", 3}, {"MEDIA_VIDEO_STALE_PROCESSING_SECONDS", 1800}, {"MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS", 5}, {"MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES", 25}, {"MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP", 10000}, {"AVATAR_UPLOAD_RATE_LIMIT", 20}, {"AVATAR_UPLOAD_RATE_WINDOW_SECONDS", 3600},
		{"DEFERRED_TASKS_BATCH", 100}, {"DEFERRED_TASKS_MAX_ATTEMPTS", 5},
	}
	for _, setting := range integers {
		d.fixedInt(setting.name, setting.value)
	}
	for _, setting := range []struct {
		name  string
		value bool
	}{{"CHILD_PUBLIC_VENUES_ONLY", true}, {"PROGRESSION_AVATAR_PUBLIC", false}, {"MEDIA_REQUIRE_SCANNER", true}, {"MEDIA_ATTACHMENTS_ENABLED", true}} {
		d.fixedBool(setting.name, setting.value)
	}
	for _, setting := range []struct{ name, value string }{
		{"CHAT_MESSAGE_POLICY", "apps.chat.policy.NudgeMessagePolicy"}, {"THREAD_REACTION_FACETS", ""},
		{"MEDIA_VIDEO_PRESET", "medium"}, {"MEDIA_VIDEO_AUDIO_BITRATE", "96k"},
	} {
		d.fixedString(setting.name, setting.value)
	}
	d.fixedList("GROUPS_USER_CREATION_COHORTS", []string{"adult"})
	d.fixedList("SUPPORT_COMPANION_COHORTS", []string{"adult"})
	d.fixedList("MEDIA_FILE_COHORTS", []string{"adult"})
	d.fixedList("MEDIA_VIDEO_COHORTS", []string{"adult"})
	d.fixedBool("DJANGO_REQUIRE_SHARED_STATE", false)
	d.fixedString("REDIS_URL", "")
	d.fixedString("SENTRY_DSN", "")
	d.fixedString("LOG_LEVEL", "INFO")
	if sample := d.get("SENTRY_TRACES_SAMPLE_RATE"); sample != "" && sample != "0" && sample != "0.0" {
		d.invalid("SENTRY_TRACES_SAMPLE_RATE")
	}
	d.fixedString("PERMISSIONS_POLICY", "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()")
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
	if layer != "channels.layers.InMemoryChannelLayer" && layer != "channels_redis.core.RedisChannelLayer" {
		d.invalid("CHANNEL_LAYER_BACKEND")
	}
	d.fixedString("ROEDU_APP_PACK", "roedu:social_media_activities_app:events_places:v1")
}
