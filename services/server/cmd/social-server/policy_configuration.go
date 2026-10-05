package main

import (
	"strconv"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/app"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

func (d *decoder) seconds(name string, fallback, floor, ceiling int) time.Duration {
	return time.Duration(d.integer(name, fallback, floor, ceiling)) * time.Second
}

func adultCohorts(d *decoder, name string) map[string]bool {
	out := map[string]bool{}
	for _, cohort := range d.list(name, []string{"adult"}) {
		if cohort != "adult" {
			d.invalid(name)
		} else {
			out[cohort] = true
		}
	}
	return out
}

func configurePolicies(d *decoder, c *runtimeConfig) {
	c.Rates = configureRates(d)
	c.RequireSharedState = d.boolean("DJANGO_REQUIRE_SHARED_STATE", false)
	c.CatalogPolicy = catalog.DefaultPolicy()
	p := &c.CatalogPolicy
	p.ClosureReportThreshold = d.integer("CLOSURE_REPORT_THRESHOLD", 3, 1, 3)
	p.OpenNowReportThreshold = d.integer("OPEN_NOW_REPORT_THRESHOLD", 3, 1, 3)
	p.EventReportThreshold = d.integer("EVENT_REPORT_THRESHOLD", 3, 1, 3)
	p.ClosureReportDecay = d.seconds("CLOSURE_REPORT_DECAY_SECONDS", 1209600, 1209600, 31536000)
	p.OpenNowReportDecay = d.seconds("OPEN_NOW_REPORT_DECAY_SECONDS", 1209600, 1209600, 31536000)
	p.EventReportDecay = d.seconds("EVENT_REPORT_DECAY_SECONDS", 1209600, 1209600, 31536000)
	p.FactQuorum = d.integer("FACT_QUORUM", 3, 3, 100)
	p.CorrectionQuorum = d.integer("CORRECTION_QUORUM", 3, 3, 100)
	p.EdgeQuorum = d.integer("EDGE_QUORUM", 3, 3, 100)
	c.SocialPolicy = social.DefaultPolicyConfig()
	s := &c.SocialPolicy
	for _, v := range []struct {
		name     string
		out      *int
		min, max int
	}{
		{"CHAT_MAX_LENGTH", &s.ChatMaxLength, 1, 4000},
		{"SOCIAL_THREAD_POST_LIMIT", &s.ThreadPostLimit, 1, 1000},
		{"SOCIAL_MEMBERSHIP_LIST_LIMIT", &s.MembershipListLimit, 1, 1000},
		{"COMMUNITY_ACTIVITIES_PAGE_SIZE", &s.CommunityActivitiesPageSize, 1, 1000},
		{"SERIES_SPAWN_LEAD_DAYS", &s.SeriesSpawnLeadDays, 1, 90},
		{"SERIES_SPAWN_BATCH", &s.SeriesSpawnBatch, 1, 1000},
		{"INTEREST_LIFETIME_DAYS", &s.InterestLifetimeDays, 1, 90},
		{"INTEREST_THRESHOLD", &s.InterestThreshold, 3, 1000},
		{"ARRIVAL_WINDOW_BEFORE_HOURS", &s.ArrivalWindowBeforeHours, 0, 24},
		{"ARRIVAL_WINDOW_AFTER_HOURS", &s.ArrivalWindowAfterHours, 0, 6},
		{"DEPARTURE_WINDOW_AFTER_HOURS", &s.DepartureWindowAfterHours, 0, 6},
		{"ARRIVAL_RETENTION_HOURS", &s.ArrivalRetentionHours, 1, 6},
		{"PLACE_PROPOSAL_DEDUP_RADIUS_M", &s.PlaceProposalDedupRadiusM, 1, 1000},
	} {
		*v.out = d.integer(v.name, *v.out, v.min, v.max)
	}
	s.UserGroupCohorts = adultCohorts(d, "GROUPS_USER_CREATION_COHORTS")
	s.SupportCompanionCohorts = adultCohorts(d, "SUPPORT_COMPANION_COHORTS")
	if s.Validate() != nil {
		d.invalid("ARRIVAL_RETENTION_HOURS")
	}
	c.Jobs.SavedSearchMatchBatch = d.integer("SAVED_SEARCH_MATCH_BATCH", 1000, 1, 10000)
	c.Jobs.DeferredBatch = d.integer("DEFERRED_TASKS_BATCH", 100, 1, 1000)
	c.DeferredMaxAttempts = d.integer("DEFERRED_TASKS_MAX_ATTEMPTS", 5, 1, 100)
	c.SavedSearchMax = d.integer("SAVED_SEARCH_MAX_PER_USER", 20, 1, 100)
	c.UnsafeReportCooldown = d.seconds("UNSAFE_REPORT_COOLDOWN_SECONDS", 300, 1, 300)
	c.Jobs.SavedSearchNotifyLimit = d.integer("SAVED_SEARCH_NOTIFY_RATE_LIMIT", 50, 1, 10000)
	c.Jobs.SavedSearchNotifyWindow = d.seconds("SAVED_SEARCH_NOTIFY_WINDOW_SECONDS", 86400, 1, 86400)
	c.MessagingPolicy = messaging.Policy{
		MaxCiphertextBytes: d.integer("MESSAGING_MAX_CIPHERTEXT_BYTES", 65536, 1, 65536),
		MaxGroupMembers:    d.integer("MESSAGING_MAX_GROUP_MEMBERS", 256, 1, 256),
	}
	c.MediaPolicy = media.DefaultPolicyConfig()
	m := &c.MediaPolicy
	m.SignedURLTTL = d.seconds("MEDIA_SIGNED_URL_TTL", 300, 1, 300)
	m.PresignedTTL = d.seconds("MEDIA_PRESIGNED_TTL", 60, 1, 60)
	m.EphemeralMinTTL = d.seconds("MEDIA_EPHEMERAL_MIN_TTL_SECONDS", 3600, 3600, 86400)
	m.EphemeralMinTTLMinors = d.seconds("MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS", 86400, 86400, 604800)
	m.VideoMaxAttempts = d.integer("MEDIA_VIDEO_MAX_ATTEMPTS", 3, 1, 10)
	m.VideoStaleProcessing = d.seconds("MEDIA_VIDEO_STALE_PROCESSING_SECONDS", 1800, 1, 1800)
	m.AttachmentsEnabled = d.boolean("MEDIA_ATTACHMENTS_ENABLED", true)
	m.FileCohorts = adultCohorts(d, "MEDIA_FILE_COHORTS")
	m.VideoCohorts = adultCohorts(d, "MEDIA_VIDEO_COHORTS")
	m.AvatarUploadLimit = d.integer("AVATAR_UPLOAD_RATE_LIMIT", 20, 1, 10000)
	m.AvatarUploadWindow = d.seconds("AVATAR_UPLOAD_RATE_WINDOW_SECONDS", 3600, 1, 86400)
	m.PerceptualProfileScanCap = d.integer("MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP", 10000, 10000, 100000)
	mc := &c.App.Media
	mc.ImageQuality = d.integer("MEDIA_IMAGE_QUALITY", 0, 0, 100)
	mc.AttachmentMaxBytes = int64(d.integer("MEDIA_ATTACHMENT_MAX_BYTES", 7<<20, 1, 7<<20))
	mc.VideoCRF = d.integer("MEDIA_VIDEO_CRF", 23, 18, 40)
	mc.VideoPreset = d.value("MEDIA_VIDEO_PRESET", "medium")
	mc.VideoAudioBitrate = d.value("MEDIA_VIDEO_AUDIO_BITRATE", "96k")
	mc.VideoFrameScanInterval = d.seconds("MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS", 5, 1, 5)
	mc.VideoFrameScanMaxFrames = d.integer("MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES", 25, 25, 100)
	if err := mc.ValidateEncodingPolicy(); err != nil {
		d.err = err
	}
	if m.VideoStaleProcessing <= max(mc.CommandTimeout, mc.ProbeTimeout) {
		d.invalid("MEDIA_VIDEO_STALE_PROCESSING_SECONDS")
	}
	c.App.MaxRequestBodyBytes = int64(d.integer("MAX_REQUEST_BODY_BYTES", 8<<20, 1, 8<<20))
	c.App.DataUploadMemoryBytes = int64(d.integer("DATA_UPLOAD_MAX_MEMORY_SIZE", int(c.App.MaxRequestBodyBytes), 1, 8<<20))
	c.App.PermissionsPolicy = d.value("PERMISSIONS_POLICY", app.DefaultPermissionsPolicy)
	if !app.ValidatePermissionsPolicy(c.App.PermissionsPolicy) {
		d.invalid("PERMISSIONS_POLICY")
	}
	c.App.LogLevel = d.value("LOG_LEVEL", "INFO")
	switch c.App.LogLevel {
	case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL":
	default:
		d.invalid("LOG_LEVEL")
	}
	c.App.AccountRetention.ArrivalHours = s.ArrivalRetentionHours
	c.App.AccountRetention.AdultPhotoMinimumSeconds = int(m.EphemeralMinTTL / time.Second)
	c.App.AccountRetention.MinorPhotoMinimumSeconds = int(m.EphemeralMinTTLMinors / time.Second)
	c.ErrorReporting = ops.ErrorReporterConfig{DSN: d.get("SENTRY_DSN"), Environment: d.value("SENTRY_ENVIRONMENT", "production")}
	switch c.ErrorReporting.Environment {
	case "production", "staging", "development":
	default:
		d.invalid("SENTRY_ENVIRONMENT")
	}
	if err := ops.ValidateErrorReporterConfig(c.ErrorReporting); err != nil {
		d.invalid("SENTRY_DSN")
	}
	if d.isSet("SENTRY_TRACES_SAMPLE_RATE") {
		n, err := strconv.ParseFloat(d.get("SENTRY_TRACES_SAMPLE_RATE"), 64)
		if err != nil || n != 0 {
			d.invalid("SENTRY_TRACES_SAMPLE_RATE")
		}
	}
}
