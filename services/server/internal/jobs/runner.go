package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"time"
)

var DueNames = []string{"purge_messaging", "purge_expired_attachments", "transcode_videos", "purge_read_notifications", "lift_suspensions", "purge_guardian_invites", "auto_complete_activities", "expire_arrivals", "expire_interest", "send_activity_reminders", "rsvp_finalize_nudge", "organizer_prep_nudge", "supervisor_needed_nudge", "generate_communities", "reverify_sweep", "consent_renewal_sweep", "spawn_due_series", "match_saved_searches", "sync_event_feeds", "sync_roedu", "expire_api_tokens", "indexnow_batch_submit", "export_agent_snapshot", "recompute_post_sentiment", "evaluate_concerns", "purge_stale_reaction_rows", "process_deferred_tasks"}

type Handler func(context.Context, map[string]json.RawMessage) (any, error)
type Config struct {
	SavedSearchNotifyLimit                                                                                                                                         int
	SavedSearchNotifyWindow                                                                                                                                        time.Duration
	SavedSearchMatchBatch, DeferredBatch                                                                                                                           int
	Social                                                                                                                                                         *social.Service
	Media                                                                                                                                                          *media.Service
	Safety                                                                                                                                                         *safety.Service
	Accounts                                                                                                                                                       *accounts.Service
	Now                                                                                                                                                            func() time.Time
	MessagingRetentionDays, NotificationRetentionDays, NotificationBatch, APITokenMaxAgeDays, ReminderHours, ReverifyReminderDays, ConsentReminderDays, SweepBatch int
	Exporter                                                                                                                                                       Handler
	Heartbeat                                                                                                                                                      func(context.Context) bool
	DeleteBlob                                                                                                                                                     func(context.Context, string) error
	BlobBatch                                                                                                                                                      int
	FetchFeed                                                                                                                                                      func(context.Context, string) ([]byte, error)
	Roedu                                                                                                                                                          *RoeduClient
	RoeduSyncEnabled                                                                                                                                               bool
	RoeduCity                                                                                                                                                      string
	IndexNowEnabled                                                                                                                                                bool
	IndexNowKey, SiteBaseURL                                                                                                                                       string
	IndexNowWindowHours, IndexNowMaxURLs                                                                                                                           int
	IndexNowHTTP                                                                                                                                                   *http.Client
	AgentSnapshotDir                                                                                                                                               string
	ResolvePlaceCovers                                                                                                                                             Handler
	Commons                                                                                                                                                        *CommonsClient
	ImportCover                                                                                                                                                    CoverImporter
	JobTimeout                                                                                                                                                     time.Duration
}

func DefaultConfig() Config {
	return Config{SavedSearchMatchBatch: 1000, DeferredBatch: 100, SavedSearchNotifyLimit: 50, SavedSearchNotifyWindow: 24 * time.Hour, MessagingRetentionDays: 0, NotificationRetentionDays: 180, NotificationBatch: 1000, APITokenMaxAgeDays: 90, ReminderHours: 24, ReverifyReminderDays: 14, ConsentReminderDays: 14, SweepBatch: 1000, BlobBatch: 200, RoeduCity: "Cluj-Napoca", JobTimeout: 5 * time.Minute}
}

type Runner struct {
	DB       *pgxpool.Pool
	Config   Config
	Queue    *ops.Queue
	handlers map[string]Handler
}

func New(db *pgxpool.Pool, config Config) *Runner {
	if config.SavedSearchNotifyLimit == 0 {
		config.SavedSearchNotifyLimit = 50
	}
	if config.SavedSearchNotifyWindow == 0 {
		config.SavedSearchNotifyWindow = 24 * time.Hour
	}
	if config.SavedSearchMatchBatch == 0 {
		config.SavedSearchMatchBatch = 1000
	}
	if config.DeferredBatch == 0 {
		config.DeferredBatch = 100
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.JobTimeout <= 0 {
		config.JobTimeout = 5 * time.Minute
	}
	if config.RoeduCity == "" {
		config.RoeduCity = "Cluj-Napoca"
	}
	if config.NotificationBatch == 0 {
		config.NotificationBatch = 1000
	}
	if config.APITokenMaxAgeDays == 0 {
		config.APITokenMaxAgeDays = 90
	}
	if config.ReminderHours == 0 {
		config.ReminderHours = 24
	}
	if config.SweepBatch == 0 {
		config.SweepBatch = 1000
	}
	if config.ReverifyReminderDays == 0 {
		config.ReverifyReminderDays = 14
	}
	if config.ConsentReminderDays == 0 {
		config.ConsentReminderDays = 14
	}
	if config.BlobBatch == 0 {
		config.BlobBatch = 200
	}
	r := &Runner{DB: db, Config: config, Queue: ops.NewQueue(db), handlers: map[string]Handler{}}
	if r.Config.ImportCover == nil && r.Config.Media != nil {
		r.Config.ImportCover = r.Config.Media.ImportLicensedPlaceCover
	}
	if r.Config.ResolvePlaceCovers == nil {
		r.Config.ResolvePlaceCovers = r.ResolveCovers
	}
	r.Queue.Now = config.Now
	r.install()
	return r
}
func (r *Runner) Register(name string, handler Handler) error {
	if handler == nil {
		return errors.New("nil native job")
	}
	if r.handlers[name] != nil {
		return errors.New("native job already registered")
	}
	r.handlers[name] = handler
	return nil
}
func (r *Runner) Run(ctx context.Context, name string, options map[string]json.RawMessage) (any, error) {
	handler := r.handlers[name]
	if handler == nil {
		return nil, errors.New("native job is not registered")
	}
	return handler(ctx, options)
}

type Result struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary any    `json:"summary,omitempty"`
}

func (r *Runner) RunDue(ctx context.Context) ([]Result, error) {
	out := []Result{}
	failures := 0
	for _, name := range DueNames {
		jobCtx, cancel := context.WithTimeout(ctx, r.Config.JobTimeout)
		value, err := r.Run(jobCtx, name, nil)
		if err == nil && jobCtx.Err() != nil {
			err = jobCtx.Err()
		}
		cancel()
		status := "ok"
		if err != nil {
			status = "failed"
			failures++
			value = nil
		}
		out = append(out, Result{name, status, value})
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	if failures > 0 {
		return out, fmt.Errorf("%d due jobs failed", failures)
	}
	if r.Config.Heartbeat != nil {
		r.Config.Heartbeat(ctx)
	}
	return out, nil
}
func missing() error { return errors.New("native job dependency unavailable") }
func (r *Runner) install() {
	r.handlers["purge_messaging"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) { return r.purgeMessages(ctx) }
	r.handlers["purge_expired_attachments"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Media == nil {
			return nil, missing()
		}
		n, err := r.Config.Media.PurgeExpiredAttachments(ctx, 500)
		if err != nil {
			return nil, err
		}
		_, err = r.Config.Media.DrainBlobDeletions(ctx, 500)
		return n, err
	}
	r.handlers["transcode_videos"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Media == nil {
			return nil, missing()
		}
		return r.Config.Media.ProcessPendingVideos(ctx, 2)
	}
	r.handlers["purge_read_notifications"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.NotificationRetentionDays <= 0 {
			return map[string]bool{"disabled": true}, nil
		}
		var id int64
		err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
			var err error
			id, err = r.Queue.Enqueue(ctx, tx, "notifications.retention_purge", map[string]any{"days": r.Config.NotificationRetentionDays, "batch_size": r.Config.NotificationBatch}, ops.EnqueueOptions{DedupKey: "notifications:retention_purge"})
			return err
		})
		return map[string]int64{"task_id": id}, err
	}
	r.handlers["lift_suspensions"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Safety == nil {
			return nil, missing()
		}
		return r.Config.Safety.LiftSuspensions(ctx)
	}
	r.handlers["purge_guardian_invites"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		tag, err := r.DB.Exec(ctx, `DELETE FROM accounts_guardianlinkinvite WHERE expires_at<$1`, r.Config.Now())
		return tag.RowsAffected(), err
	}
	r.handlers["auto_complete_activities"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.AutoComplete(ctx, r.Config.Now(), 12*time.Hour)
	}
	r.handlers["expire_arrivals"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.ExpirePresence(ctx, r.Config.Now(), r.Config.Social.PresenceRetention())
	}
	r.handlers["expire_interest"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.ExpireGauges(ctx, r.Config.Now())
	}
	r.handlers["send_activity_reminders"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) { return r.Reminders(ctx) }
	r.handlers["rsvp_finalize_nudge"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.NudgeRSVP(ctx, r.Config.Now())
	}
	r.handlers["organizer_prep_nudge"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.NudgeOrganizers(ctx, r.Config.Now())
	}
	r.handlers["supervisor_needed_nudge"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.NudgeSupervisors(ctx, r.Config.Now())
	}
	r.handlers["generate_communities"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.GenerateCommunities(ctx, r.Config.Now())
	}
	r.handlers["reverify_sweep"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Accounts == nil {
			return nil, missing()
		}
		return r.Config.Accounts.RunReverifySweep(ctx, r.Config.Now(), r.Config.ReverifyReminderDays, r.Config.SweepBatch)
	}
	r.handlers["consent_renewal_sweep"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Accounts == nil {
			return nil, missing()
		}
		return r.Config.Accounts.RunConsentRenewalSweep(ctx, r.Config.Now(), r.Config.ConsentReminderDays, r.Config.SweepBatch)
	}
	r.handlers["spawn_due_series"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.SpawnDueSeries(ctx, r.Config.Now())
	}
	r.handlers["match_saved_searches"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) { return r.MatchSavedSearches(ctx) }
	r.handlers["sync_event_feeds"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) { return r.SyncFeeds(ctx) }
	r.handlers["sync_roedu"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) { return r.SyncRoedu(ctx) }
	r.handlers["expire_api_tokens"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		tag, err := r.DB.Exec(ctx, `DELETE FROM authtoken_token WHERE created<$1`, r.Config.Now().Add(-time.Duration(r.Config.APITokenMaxAgeDays)*24*time.Hour))
		return tag.RowsAffected(), err
	}
	r.handlers["indexnow_batch_submit"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) { return r.IndexNow(ctx) }
	r.handlers["export_agent_snapshot"] = func(ctx context.Context, o map[string]json.RawMessage) (any, error) {
		if r.Config.Exporter != nil {
			return r.Config.Exporter(ctx, o)
		}
		return r.ExportAgentSnapshot(ctx, o)
	}
	r.handlers["recompute_post_sentiment"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.RecomputeSentiment(ctx, r.Config.Now())
	}
	r.handlers["evaluate_concerns"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.EvaluateConcerns(ctx, r.Config.Now())
	}
	r.handlers["purge_stale_reaction_rows"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		if r.Config.Social == nil {
			return nil, missing()
		}
		return r.Config.Social.PurgeSentimentRows(ctx, r.Config.Now())
	}
	r.handlers["process_deferred_tasks"] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
		return r.Queue.RunPending(ctx, r.Config.DeferredBatch)
	}
	r.installDeferred()
}
