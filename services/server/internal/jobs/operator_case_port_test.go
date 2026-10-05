package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func caseEnqueue(t *testing.T, r *Runner, kind string, payload map[string]any, options ops.EnqueueOptions) int64 {
	t.Helper()
	var id int64
	err := platform.Transaction(context.Background(), r.DB, func(tx pgx.Tx) error {
		var err error
		id, err = r.Queue.Enqueue(context.Background(), tx, kind, payload, options)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func caseTaskCount(t *testing.T, r *Runner, where string) int {
	t.Helper()
	var n int
	if err := r.DB.QueryRow(context.Background(), `SELECT count(*) FROM ops_deferredtask WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostgresOperatorCaseQueueEnqueueAtomicDedupAndDelay(t *testing.T) {
	for _, name := range []string{"pending_row", "unregistered_fast_failure", "transaction_rollback", "future_delay", "pending_dedup", "terminal_dedup", "database_pending_constraint", "empty_dedup_keys"} {
		t.Run(name, func(t *testing.T) {
			r := jobFixture(t)
			ctx := context.Background()
			if err := r.Queue.Register("case.ok", func(context.Context, pgx.Tx, map[string]json.RawMessage) error { return nil }); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "pending_row":
				id := caseEnqueue(t, r, "case.ok", map[string]any{"user_id": 7}, ops.EnqueueOptions{})
				var status, kind string
				var payload []byte
				var attempts int
				if err := r.DB.QueryRow(ctx, `SELECT status,kind,payload,attempts FROM ops_deferredtask WHERE id=$1`, id).Scan(&status, &kind, &payload, &attempts); err != nil {
					t.Fatal(err)
				}
				var data struct {
					User int `json:"user_id"`
				}
				if status != "PENDING" || kind != "case.ok" || attempts != 0 || json.Unmarshal(payload, &data) != nil || data.User != 7 || caseTaskCount(t, r, "true") != 1 {
					t.Fatal("enqueue fields/default status differ")
				}
			case "unregistered_fast_failure":
				err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
					_, err := r.Queue.Enqueue(ctx, tx, "case.missing", nil, ops.EnqueueOptions{})
					return err
				})
				if err == nil || caseTaskCount(t, r, "true") != 0 {
					t.Fatal("unregistered enqueue wrote a row")
				}
			case "transaction_rollback":
				err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
					if _, err := r.Queue.Enqueue(ctx, tx, "case.ok", nil, ops.EnqueueOptions{}); err != nil {
						return err
					}
					return errors.New("synthetic business rollback")
				})
				if err == nil || caseTaskCount(t, r, "true") != 0 {
					t.Fatal("business rollback committed a deferred task")
				}
			case "future_delay":
				before := time.Now()
				id := caseEnqueue(t, r, "case.ok", nil, ops.EnqueueOptions{Delay: time.Hour})
				var available time.Time
				if err := r.DB.QueryRow(ctx, `SELECT available_at FROM ops_deferredtask WHERE id=$1`, id).Scan(&available); err != nil || !available.After(before.Add(59*time.Minute)) {
					t.Fatal("enqueue delay not retained")
				}
			case "pending_dedup":
				a := caseEnqueue(t, r, "case.ok", map[string]any{"u": 1}, ops.EnqueueOptions{DedupKey: "user:1"})
				b := caseEnqueue(t, r, "case.ok", map[string]any{"u": 1}, ops.EnqueueOptions{DedupKey: "user:1"})
				if a != b || caseTaskCount(t, r, "status='PENDING'") != 1 {
					t.Fatal("pending dedup did not collapse")
				}
			case "terminal_dedup":
				a := caseEnqueue(t, r, "case.ok", nil, ops.EnqueueOptions{DedupKey: "user:1"})
				if summary, err := r.Queue.RunPending(ctx, 10); err != nil || summary.Done != 1 {
					t.Fatal("first dedup task did not complete")
				}
				b := caseEnqueue(t, r, "case.ok", nil, ops.EnqueueOptions{DedupKey: "user:1"})
				if a == b || caseTaskCount(t, r, "status='PENDING'") != 1 || caseTaskCount(t, r, "status='DONE'") != 1 {
					t.Fatal("terminal task suppressed fresh enqueue")
				}
			case "database_pending_constraint":
				caseEnqueue(t, r, "case.ok", nil, ops.EnqueueOptions{DedupKey: "d"})
				_, err := r.DB.Exec(ctx, `INSERT INTO ops_deferredtask(kind,payload,status,attempts,max_attempts,available_at,dedup_key,last_error,created_at) VALUES('case.ok','{}','PENDING',0,5,now(),'d','',now())`)
				if err == nil || caseTaskCount(t, r, "true") != 1 {
					t.Fatal("database admitted two same-key pending tasks")
				}
			case "empty_dedup_keys":
				a := caseEnqueue(t, r, "case.ok", nil, ops.EnqueueOptions{})
				b := caseEnqueue(t, r, "case.ok", nil, ops.EnqueueOptions{})
				if a == b || caseTaskCount(t, r, "true") != 2 {
					t.Fatal("empty dedup keys collided")
				}
			}
		})
	}
}

type operatorCaseHandlerFailure struct{}

func (operatorCaseHandlerFailure) Error() string { return "synthetic private handler details" }

func TestPostgresOperatorCaseQueueExecutionIsolationRetryAndOrdering(t *testing.T) {
	for _, name := range []string{"done_counter_and_timestamp", "payload_copy", "handler_savepoint_rollback", "retry_then_failed", "future_backoff", "not_yet_due", "oldest_first", "batch_limit"} {
		t.Run(name, func(t *testing.T) {
			r := jobFixture(t)
			ctx := context.Background()
			seen := []string{}
			calls := 0
			handler := func(ctx context.Context, tx pgx.Tx, body map[string]json.RawMessage) error {
				calls++
				switch name {
				case "done_counter_and_timestamp":
					var value int
					if json.Unmarshal(body["x"], &value) != nil || value != 1 {
						t.Fatal("handler payload changed")
					}
				case "payload_copy":
					body["injected"] = json.RawMessage(`true`)
				case "handler_savepoint_rollback":
					if _, err := tx.Exec(ctx, `INSERT INTO ops_deferredtask(kind,payload,status,attempts,max_attempts,available_at,dedup_key,last_error,created_at) VALUES('case.sentinel','{}','PENDING',0,1,now(),'','',now())`); err != nil {
						return err
					}
					return operatorCaseHandlerFailure{}
				case "retry_then_failed", "future_backoff":
					return operatorCaseHandlerFailure{}
				case "oldest_first":
					var value string
					if json.Unmarshal(body["n"], &value) != nil {
						t.Fatal("ordered payload")
					}
					seen = append(seen, value)
				}
				return nil
			}
			if err := r.Queue.Register("case.run", handler); err != nil {
				t.Fatal(err)
			}
			options := ops.EnqueueOptions{}
			payload := map[string]any{"x": 1}
			limit := 10
			if name == "retry_then_failed" {
				r.Queue.BackoffBase = 0
				options.MaxAttempts = 2
			}
			if name == "handler_savepoint_rollback" {
				options.MaxAttempts = 1
			}
			if name == "not_yet_due" {
				options.Delay = time.Hour
			}
			if name == "oldest_first" {
				at := time.Now().Add(-5 * time.Second)
				caseEnqueue(t, r, "case.run", map[string]any{"n": "b"}, ops.EnqueueOptions{AvailableAt: &at})
				at = at.Add(-5 * time.Second)
				options.AvailableAt = &at
				payload = map[string]any{"n": "a"}
			}
			id := caseEnqueue(t, r, "case.run", payload, options)
			if name == "batch_limit" {
				caseEnqueue(t, r, "case.run", payload, options)
				caseEnqueue(t, r, "case.run", payload, options)
				limit = 2
			}
			before := time.Now()
			summary, err := r.Queue.RunPending(ctx, limit)
			if err != nil {
				t.Fatal(err)
			}
			var status, diagnostic string
			var attempts int
			var finished *time.Time
			var available time.Time
			var stored []byte
			if err = r.DB.QueryRow(ctx, `SELECT status,attempts,finished_at,available_at,last_error,payload FROM ops_deferredtask WHERE id=$1`, id).Scan(&status, &attempts, &finished, &available, &diagnostic, &stored); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "done_counter_and_timestamp":
				if summary != (ops.TaskSummary{Claimed: 1, Done: 1}) || calls != 1 || status != "DONE" || attempts != 1 || finished == nil {
					t.Fatal("successful execution counters/state differ")
				}
			case "payload_copy":
				var body map[string]json.RawMessage
				if json.Unmarshal(stored, &body) != nil || body["injected"] != nil || string(body["x"]) != "1" {
					t.Fatal("mutated handler payload corrupted stored row")
				}
			case "handler_savepoint_rollback":
				if status != "FAILED" || summary.Failed != 1 || caseTaskCount(t, r, "kind='case.sentinel'") != 0 {
					t.Fatal("failed handler business writes survived savepoint")
				}
			case "retry_then_failed":
				if calls != 2 || summary != (ops.TaskSummary{Claimed: 2, Retried: 1, Failed: 1}) || status != "FAILED" || attempts != 2 || finished == nil || !strings.Contains(diagnostic, "operatorCaseHandlerFailure") || strings.Contains(diagnostic, "private handler details") {
					t.Fatal("retry exhaustion/counters/class-only diagnostic differ")
				}
			case "future_backoff":
				if summary != (ops.TaskSummary{Claimed: 1, Retried: 1}) || status != "PENDING" || attempts != 1 || !available.After(before) {
					t.Fatal("retry was not deferred to future")
				}
			case "not_yet_due":
				if summary != (ops.TaskSummary{}) || calls != 0 || attempts != 0 || status != "PENDING" {
					t.Fatal("future task was claimed")
				}
			case "oldest_first":
				if !reflect.DeepEqual(seen, []string{"a", "b"}) || summary.Done != 2 {
					t.Fatal("available_at ordering differs")
				}
			case "batch_limit":
				if summary.Claimed != 2 || summary.Done != 2 || calls != 2 || caseTaskCount(t, r, "status='PENDING'") != 1 {
					t.Fatal("batch consumed more than declared limit")
				}
			}
		})
	}
}

func TestPostgresOperatorCaseMissingHandlerDiagnosticAndMalformedPriority(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	if err := r.Queue.Register("case.vanished", func(context.Context, pgx.Tx, map[string]json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	id := caseEnqueue(t, r, "case.vanished", map[string]any{"private": "synthetic-only"}, ops.EnqueueOptions{MaxAttempts: 1})
	queue := ops.NewQueue(r.DB)
	summary, err := queue.RunPending(ctx, 10)
	if err != nil || summary != (ops.TaskSummary{Claimed: 1, Failed: 1}) {
		t.Fatal("missing handler was not one bounded failure")
	}
	var status, diagnostic string
	var attempts int
	var finished *time.Time
	if err = r.DB.QueryRow(ctx, `SELECT status,attempts,finished_at,last_error FROM ops_deferredtask WHERE id=$1`, id).Scan(&status, &attempts, &finished, &diagnostic); err != nil || status != "FAILED" || attempts != 1 || finished == nil || diagnostic != "no handler for deferred task" {
		t.Fatal("safe missing-handler diagnosis lost")
	}
	var malformed int64
	if err = r.DB.QueryRow(ctx, `INSERT INTO ops_deferredtask(kind,payload,status,attempts,max_attempts,available_at,dedup_key,last_error,created_at) VALUES('case.missing','["synthetic private payload"]','PENDING',0,1,now(),'','',now()) RETURNING id`).Scan(&malformed); err != nil {
		t.Fatal(err)
	}
	if summary, err = queue.RunPending(ctx, 10); err != nil || summary.Failed != 1 {
		t.Fatal("malformed queue payload wasn't bounded")
	}
	if err = r.DB.QueryRow(ctx, `SELECT last_error FROM ops_deferredtask WHERE id=$1`, malformed).Scan(&diagnostic); err != nil || strings.Contains(diagnostic, "no handler") || strings.Contains(diagnostic, "private payload") || !strings.Contains(diagnostic, "json.UnmarshalTypeError") {
		t.Fatal("malformed JSON type priority or diagnostic privacy changed")
	}
}

func TestOperatorCaseQueueRegistryIsInstanceLocalAndRejectsAmbiguity(t *testing.T) {
	first, second := New(nil, DefaultConfig()), New(nil, DefaultConfig())
	want := []string{"cron.run_command", "erasure.blob_cleanup", "media.scan.dispatch", "notifications.retention_purge", "notify.activity_fanout"}
	if !reflect.DeepEqual(first.Queue.Kinds(), want) || !reflect.DeepEqual(second.Queue.Kinds(), want) {
		t.Fatal("reviewed deferred handler allowlist/repeated construction drift")
	}
	fn := func(context.Context, pgx.Tx, map[string]json.RawMessage) error { return nil }
	if first.Queue.Register("case.registry", fn) != nil || first.Queue.Register("case.registry", fn) == nil || first.Queue.Register("case.registry", func(context.Context, pgx.Tx, map[string]json.RawMessage) error { return nil }) == nil {
		t.Fatal("native registry failed to reject ambiguous duplicate")
	}
	if second.Queue.Register("case.registry", fn) != nil {
		t.Fatal("per-instance registry leaked across construction")
	}
}

func TestOperatorCaseDueInventoryFailureIsolationAndGreenHeartbeat(t *testing.T) {
	r := New(nil, DefaultConfig())
	// Read the frozen declaration as DATA; never import/execute the reference.
	reference, readErr := os.ReadFile(filepath.Join("..", "..", "..", "..", "apps", "ops", "management", "commands", "run_due_jobs.py"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	start := strings.Index(string(reference), "DUE_JOBS = (")
	if start < 0 {
		t.Fatal("reference due declaration absent")
	}
	declaration := string(reference)[start:]
	end := strings.Index(declaration, "\n)")
	if end < 0 {
		t.Fatal("reference due declaration unterminated")
	}
	expected := []string{}
	for _, match := range regexp.MustCompile(`(?m)^\s*\("([a-z_]+)",`).FindAllStringSubmatch(declaration[:end], -1) {
		expected = append(expected, match[1])
	}
	if !reflect.DeepEqual(DueNames, expected) || r.handlers["process_deferred_tasks"] == nil {
		t.Fatal("native due inventory/order or queue wiring differs from frozen source")
	}
	called := []string{}
	heartbeat := 0
	r.Config.Heartbeat = func(context.Context) bool { heartbeat++; return true }
	for _, name := range DueNames {
		current := name
		r.handlers[name] = func(context.Context, map[string]json.RawMessage) (any, error) {
			called = append(called, current)
			if current == "lift_suspensions" {
				return nil, errors.New("synthetic due failure")
			}
			return 1, nil
		}
	}
	rows, err := r.RunDue(context.Background())
	if err == nil || !reflect.DeepEqual(called, DueNames) || len(rows) != len(DueNames) || heartbeat != 0 {
		t.Fatal("failing duty skipped others or sent heartbeat")
	}
	for _, row := range rows {
		if row.Name == "lift_suspensions" && row.Status != "failed" || row.Name != "lift_suspensions" && row.Status != "ok" {
			t.Fatal("per-duty outcome changed")
		}
	}
	encoded, marshalErr := json.Marshal(rows)
	var output []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if marshalErr != nil || json.Unmarshal(encoded, &output) != nil || len(output) != len(expected) {
		t.Fatal("native structured due output lost source outcome values")
	}
	r.handlers["lift_suspensions"] = func(context.Context, map[string]json.RawMessage) (any, error) { return 1, nil }
	if _, err = r.RunDue(context.Background()); err != nil || heartbeat != 1 {
		t.Fatal("clean due tick did not heartbeat exactly once")
	}
}

func TestPostgresOperatorCaseCronDeferredAllowlistAndResultValues(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	called := 0
	r.handlers["expire_api_tokens"] = func(context.Context, map[string]json.RawMessage) (any, error) { called++; return 7, nil }
	caseEnqueue(t, r, "cron.run_command", map[string]any{"command": "expire_api_tokens"}, ops.EnqueueOptions{MaxAttempts: 1})
	summary, err := r.Queue.RunPending(ctx, 10)
	if err != nil || called != 1 || summary != (ops.TaskSummary{Claimed: 1, Done: 1}) {
		t.Fatal("reviewed cron command did not dispatch exactly once")
	}
	caseEnqueue(t, r, "cron.run_command", map[string]any{"command": "shell"}, ops.EnqueueOptions{MaxAttempts: 1})
	summary, err = r.Queue.RunPending(ctx, 10)
	if err != nil || called != 1 || summary != (ops.TaskSummary{Claimed: 1, Failed: 1}) {
		t.Fatal("unreviewed command dispatched or failure counters lost")
	}
	caseEnqueue(t, r, "cron.run_command", map[string]any{"command": "process_deferred_tasks"}, ops.EnqueueOptions{MaxAttempts: 1})
	if summary, err = r.Queue.RunPending(ctx, 10); err != nil || summary.Failed != 1 {
		t.Fatal("recursive queue drain admitted")
	}
}

func TestPostgresOperatorCaseDueReminderWindowReachesRegisteredHandler(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	r.Config.Now = func() time.Time { return now }
	r.Config.ReminderHours = 6
	uid := jobUser(t, r, "case-reminder-owner", "adult", "adult")
	actor, err := accounts.NewStore(r.DB).Actor(ctx, strconv.FormatInt(uid, 10))
	if err != nil {
		t.Fatal(err)
	}
	place := testdb.Place(t, r.DB, "Case reminder venue", "osm")
	var typ int64
	if err = r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	within, err := r.Config.Social.CreateActivity(ctx, actor, social.ActivityInput{Place: place, ActivityType: typ, Title: "Five-hour fixture", StartsAt: now.Add(5 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	beyond, err := r.Config.Social.CreateActivity(ctx, actor, social.ActivityInput{Place: place, ActivityType: typ, Title: "Seven-hour fixture", StartsAt: now.Add(7 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	reminder := r.handlers["send_activity_reminders"]
	for _, name := range DueNames {
		r.handlers[name] = func(context.Context, map[string]json.RawMessage) (any, error) { return 0, nil }
	}
	r.handlers["send_activity_reminders"] = reminder
	if _, err = r.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	var near, far int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE url=$2),count(*) FILTER(WHERE url=$3) FROM notifications_notification WHERE recipient_id=$1 AND kind='event_reminder'`, uid, "/api/social/activities/"+strconv.FormatInt(within, 10)+"/", "/api/social/activities/"+strconv.FormatInt(beyond, 10)+"/").Scan(&near, &far); err != nil || near != 1 || far != 0 {
		t.Fatal("six-hour due reminder window was ignored")
	}
}
