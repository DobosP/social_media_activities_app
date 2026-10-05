package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"regexp"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

type TaskHandler func(context.Context, pgx.Tx, map[string]json.RawMessage) error
type Queue struct {
	DB                      *pgxpool.Pool
	mu                      sync.RWMutex
	handlers                map[string]TaskHandler
	worker                  chan struct{}
	Now                     func() time.Time
	BackoffBase, BackoffMax time.Duration
	MaxAttempts             int
}

func NewQueue(db *pgxpool.Pool) *Queue {
	return &Queue{DB: db, handlers: map[string]TaskHandler{}, worker: make(chan struct{}, 1), Now: time.Now, BackoffBase: 30 * time.Second, BackoffMax: time.Hour, MaxAttempts: 5}
}

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

func (q *Queue) Register(kind string, handler TaskHandler) error {
	if !kindPattern.MatchString(kind) || handler == nil {
		return errors.New("invalid deferred handler")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.handlers[kind] != nil {
		return errors.New("deferred kind already registered")
	}
	q.handlers[kind] = handler
	return nil
}
func (q *Queue) Kinds() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := []string{}
	for key := range q.handlers {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

type EnqueueOptions struct {
	Delay       time.Duration
	AvailableAt *time.Time
	MaxAttempts int
	DedupKey    string
}

func (q *Queue) Enqueue(ctx context.Context, tx pgx.Tx, kind string, payload map[string]any, o EnqueueOptions) (int64, error) {
	q.mu.RLock()
	known := q.handlers[kind] != nil
	q.mu.RUnlock()
	if !known {
		return 0, errors.New("unregistered deferred task kind")
	}
	if utf8.RuneCountInString(o.DedupKey) > 200 {
		return 0, errors.New("invalid deferred dedup key")
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = q.MaxAttempts
		if o.MaxAttempts == 0 {
			o.MaxAttempts = 5
		}
	}
	if o.MaxAttempts < 1 || o.MaxAttempts > 100 {
		return 0, errors.New("invalid deferred attempt budget")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 64<<10 {
		return 0, errors.New("invalid deferred task payload")
	}
	when := q.Now().Add(o.Delay)
	if o.AvailableAt != nil {
		when = *o.AvailableAt
	}
	if o.DedupKey != "" {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "deferred:"+kind+":"+o.DedupKey); err != nil {
			return 0, err
		}
		var id int64
		err = tx.QueryRow(ctx, `SELECT id FROM ops_deferredtask WHERE kind=$1 AND dedup_key=$2 AND status='PENDING'`, kind, o.DedupKey).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO ops_deferredtask(kind,payload,status,attempts,max_attempts,available_at,dedup_key,last_error,created_at,started_at,finished_at) VALUES($1,$2,'PENDING',0,$3,$4,$5,'',now(),NULL,NULL) RETURNING id`, kind, raw, o.MaxAttempts, when, o.DedupKey).Scan(&id)
	return id, err
}

type TaskSummary struct{ Claimed, Done, Retried, Failed int }

func (q *Queue) RunPending(ctx context.Context, limit int) (TaskSummary, error) {
	if limit < 0 || limit > 10000 {
		return TaskSummary{}, errors.New("invalid deferred batch")
	}
	// A cron task may call a service which acquires another database connection.
	// One drainer per Queue keeps those calls from exhausting this process's
	// small connection pool while holding all its task-row locks. Independent
	// replicas still claim different rows through SKIP LOCKED.
	select {
	case q.worker <- struct{}{}:
		defer func() { <-q.worker }()
	case <-ctx.Done():
		return TaskSummary{}, ctx.Err()
	}
	summary := TaskSummary{}
	for i := 0; i < limit; i++ {
		outcome, found, err := q.runOne(ctx)
		if err != nil {
			return summary, err
		}
		if !found {
			break
		}
		summary.Claimed++
		switch outcome {
		case "done":
			summary.Done++
		case "retried":
			summary.Retried++
		case "failed":
			summary.Failed++
		}
	}
	return summary, nil
}
func (q *Queue) runOne(ctx context.Context) (string, bool, error) {
	tx, err := q.DB.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var id int64
	var kind string
	var raw []byte
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT id,kind,payload,attempts,max_attempts FROM ops_deferredtask WHERE status='PENDING' AND available_at<=$1 ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, q.Now()).Scan(&id, &kind, &raw, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	attempts++
	if _, err = tx.Exec(ctx, `SAVEPOINT deferred_handler`); err != nil {
		return "", true, err
	}
	q.mu.RLock()
	handler := q.handlers[kind]
	q.mu.RUnlock()
	var payload map[string]json.RawMessage
	handlerErr := json.Unmarshal(raw, &payload)
	missingHandler := false
	if handlerErr == nil {
		if handler == nil {
			missingHandler = true
			handlerErr = errors.New("missing deferred handler")
		} else {
			handlerErr = handler(ctx, tx, payload)
		}
	}
	outcome := "done"
	if handlerErr != nil {
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT deferred_handler`); err != nil {
			return "", true, err
		}
		status := "PENDING"
		var finish any
		delay := q.BackoffBase
		for i := 1; i < attempts && delay < q.BackoffMax; i++ {
			delay = min(delay*2, q.BackoffMax)
		}
		if attempts >= maxAttempts {
			status = "FAILED"
			finish = q.Now()
			outcome = "failed"
		} else {
			outcome = "retried"
		}
		diagnostic := fmt.Sprintf("%T: deferred handler failed", handlerErr)
		if missingHandler {
			diagnostic = "no handler for deferred task"
		}
		_, err = tx.Exec(ctx, `UPDATE ops_deferredtask SET status=$2,attempts=$3,started_at=COALESCE(started_at,$4),finished_at=$5,available_at=$6,last_error=$7 WHERE id=$1`, id, status, attempts, q.Now(), finish, q.Now().Add(delay), diagnostic)
	} else {
		_, err = tx.Exec(ctx, `UPDATE ops_deferredtask SET status='DONE',attempts=$2,started_at=COALESCE(started_at,$3),finished_at=$3,last_error='' WHERE id=$1`, id, attempts, q.Now())
	}
	if err != nil {
		return "", true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", true, err
	}
	return outcome, true, nil
}
