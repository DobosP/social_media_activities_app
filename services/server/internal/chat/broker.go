package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnavailable = errors.New("live delivery unavailable")

type subscription struct {
	kind   string
	room   int64
	events chan Event
	done   chan struct{}
	once   sync.Once
}

func (s *subscription) close() { s.once.Do(func() { close(s.done) }) }

type Broker struct {
	db                       *pgxpool.Pool
	mu                       sync.Mutex
	subscribers              map[*subscription]struct{}
	ready                    atomic.Bool
	maxTotal, maxRoom, queue int
	typing                   *typingCoalescer
}

func NewBroker(db *pgxpool.Pool) *Broker {
	return &Broker{db: db, subscribers: map[*subscription]struct{}{}, maxTotal: 1024, maxRoom: 256, queue: 32, typing: newTypingCoalescer(typingWindow, 4096)}
}
func (b *Broker) Ready() bool { return b.ready.Load() }
func (b *Broker) subscribe(kind string, id int64) (*subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ready.Load() || len(b.subscribers) >= b.maxTotal {
		return nil, ErrUnavailable
	}
	n := 0
	for s := range b.subscribers {
		if s.kind == kind && s.room == id {
			n++
		}
	}
	if n >= b.maxRoom {
		return nil, ErrUnavailable
	}
	s := &subscription{kind: kind, room: id, events: make(chan Event, b.queue), done: make(chan struct{})}
	b.subscribers[s] = struct{}{}
	return s, nil
}
func (b *Broker) unsubscribe(s *subscription) {
	b.mu.Lock()
	delete(b.subscribers, s)
	b.mu.Unlock()
	s.close()
}
// dispatch never invalidates a socket for a transient typing event: typing is
// dropped for a subscriber whose queue is at least half full, which keeps the
// other half for durable events. Only a durable event that cannot be queued
// closes the subscriber so its client reloads durable history.
func (b *Broker) dispatch(event Event) {
	if !ValidEvent(event) {
		return
	}
	droppable := transient(event)
	if droppable {
		event.shared = &typingResolution{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subscribers {
		if s.kind != event.Kind || s.room != event.RoomID {
			continue
		}
		if droppable && 2*len(s.events) >= cap(s.events) {
			continue
		}
		select {
		case <-s.done:
			delete(b.subscribers, s)
		case s.events <- event:
		default:
			if !droppable {
				s.close()
				delete(b.subscribers, s)
			}
		}
	}
}
func (b *Broker) reset() {
	b.ready.Store(false)
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subscribers {
		s.close()
		delete(b.subscribers, s)
	}
}
func decodeEvent(raw string) (Event, error) {
	var event Event
	if len(raw) > 1024 {
		return event, ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if d.Decode(&event) != nil || d.Decode(new(any)) != io.EOF || !ValidEvent(event) {
		return Event{}, ErrUnavailable
	}
	return event, nil
}

// Run reserves one bounded pool connection for LISTEN. Notifications lost during
// a disconnect invalidate existing sockets; clients reload durable REST history.
// Backoff is cancellable and no reconnect attempt creates an extra listener.
func (b *Broker) Run(ctx context.Context) error {
	if b.db == nil {
		return ErrUnavailable
	}
	delay := time.Second
	for {
		if ctx.Err() != nil {
			b.reset()
			return ctx.Err()
		}
		e := b.listen(ctx)
		b.reset()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = e
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(30*time.Second, delay*2)
	}
}
func (b *Broker) listen(ctx context.Context) error {
	conn, e := b.db.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	var schema string
	if e = conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		return e
	}
	if _, e = conn.Exec(ctx, `LISTEN `+pgx.Identifier{Channel(schema)}.Sanitize()); e != nil {
		return e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.Exec(cleanup, `UNLISTEN *`)
	}()
	b.ready.Store(true)
	for {
		notification, e := conn.Conn().WaitForNotification(ctx)
		if e != nil {
			return e
		}
		event, e := decodeEvent(notification.Payload)
		if e == nil {
			b.dispatch(event)
		}
	}
}

// Transient typing travels through the same schema namespace but has no durable
// user/presence row. Failure is a transport no-op at its caller.
func (b *Broker) Publish(ctx context.Context, event Event) error {
	tx, e := b.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = Publish(ctx, tx, event); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
