package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormerFixedOverridesAreValidatedAndWired(t *testing.T) {
	c,err:=configuration(fixtureEnv(map[string]string{
		"CLOSURE_REPORT_THRESHOLD":"1","CLOSURE_REPORT_DECAY_SECONDS":"2592000","FACT_QUORUM":"4","EDGE_QUORUM":"5","CORRECTION_QUORUM":"6",
		"CHAT_MAX_LENGTH":"500","SOCIAL_THREAD_POST_LIMIT":"10","SOCIAL_MEMBERSHIP_LIST_LIMIT":"20","COMMUNITY_ACTIVITIES_PAGE_SIZE":"30",
		"SERIES_SPAWN_LEAD_DAYS":"3","SERIES_SPAWN_BATCH":"25","INTEREST_THRESHOLD":"4","INTEREST_LIFETIME_DAYS":"7",
		"SAVED_SEARCH_MAX_PER_USER":"4","SAVED_SEARCH_MATCH_BATCH":"10","SAVED_SEARCH_NOTIFY_RATE_LIMIT":"2","SAVED_SEARCH_NOTIFY_WINDOW_SECONDS":"3600",
		"MESSAGING_MAX_GROUP_MEMBERS":"4","MESSAGING_MAX_CIPHERTEXT_BYTES":"1024","ARRIVAL_RETENTION_HOURS":"4","UNSAFE_REPORT_COOLDOWN_SECONDS":"60",
		"MEDIA_ATTACHMENTS_ENABLED":"false","MEDIA_FILE_COHORTS":"","MEDIA_VIDEO_COHORTS":"","MEDIA_SIGNED_URL_TTL":"100","MEDIA_PRESIGNED_TTL":"30",
		"MEDIA_IMAGE_QUALITY":"90","MEDIA_VIDEO_CRF":"30","MEDIA_VIDEO_PRESET":"fast","MEDIA_VIDEO_AUDIO_BITRATE":"64k","MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS":"1","MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES":"90",
		"MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP":"12000","MEDIA_VIDEO_MAX_ATTEMPTS":"2","MEDIA_VIDEO_STALE_PROCESSING_SECONDS":"900","AVATAR_UPLOAD_RATE_LIMIT":"3","AVATAR_UPLOAD_RATE_WINDOW_SECONDS":"60",
		"DEFERRED_TASKS_BATCH":"5","DEFERRED_TASKS_MAX_ATTEMPTS":"2","THREAD_POST_RATE_LIMIT":"2","THREAD_POST_RATE_WINDOW_SECONDS":"300",
		"MAX_REQUEST_BODY_BYTES":"1024","DATA_UPLOAD_MAX_MEMORY_SIZE":"512","LOG_LEVEL":"ERROR",
	}),fixtureOptions(),func(name string)bool{return name=="MEDIA_FILE_COHORTS" || name=="MEDIA_VIDEO_COHORTS"})
	if err!=nil {t.Fatal(err)}
	if c.CatalogPolicy.FactQuorum!=4 || !strings.Contains(c.CatalogPolicy.PlaceSQL(),"count(*)>=1") || c.SocialPolicy.ChatMaxLength!=500 || c.MessagingPolicy.MaxCiphertextBytes!=1024 || c.SavedSearchMax!=4 {t.Fatal("domain policy override lost")}
	if c.MediaPolicy.AttachmentsEnabled || len(c.MediaPolicy.VideoCohorts)!=0 || c.App.Media.ImageQuality!=90 || c.App.Media.VideoFrameScanMaxFrames!=90 || c.MediaPolicy.PerceptualProfileScanCap!=12000 {t.Fatal("media policy override lost")}
	if c.Rates["social/thread_post"].Limit!=2 || c.Rates["social/thread_post"].Window!=5*time.Minute || c.App.DataUploadMemoryBytes!=512 || c.DeferredMaxAttempts!=2 || c.Jobs.SavedSearchNotifyLimit!=2 {t.Fatal("operations policy override lost")}
}

func TestNondefaultPoliciesCannotWeakenFloorsOrClaimRetiredBackends(t *testing.T) {
	for _, entry:=range []struct{name,value string}{
		{"CLOSURE_REPORT_DECAY_SECONDS","1209599"},{"EVENT_REPORT_THRESHOLD","4"},{"FACT_QUORUM","2"},
		{"MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS","86399"},{"MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP","9999"},{"MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS","6"},
		{"MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES","17"},{"MEDIA_VIDEO_STALE_PROCESSING_SECONDS","600"},{"MEDIA_FILE_COHORTS","teen"},{"ARRIVAL_RETENTION_HOURS","7"},
		{"MAX_REQUEST_BODY_BYTES","8388609"},{"DATA_UPLOAD_MAX_MEMORY_SIZE","0"},{"CHANNEL_LAYER_BACKEND","channels_redis.core.RedisChannelLayer"},
	} {
		_,err:=configuration(fixtureEnv(map[string]string{entry.name:entry.value}),fixtureOptions())
		if err==nil || !strings.Contains(err.Error(),entry.name) {t.Fatal("invalid setting accepted",entry.name,err)}
	}
	_,err:=databaseConfig(fixtureEnv(map[string]string{"DATABASE_URL":"postgres://fixture:synthetic@127.0.0.1/fixture","DB_POOL_TIMEOUT":"10"}))
	if err==nil || !strings.Contains(err.Error(),"DB_POOL_TIMEOUT") {t.Fatal("retired queue timeout silently accepted")}
}
