package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
)

type Authority func(context.Context, *http.Request) (platform.Actor, error)
type Adapter struct {
	Authorize func(context.Context, platform.Actor, int64) (bool, error)
	Receive   func(context.Context, platform.Actor, int64, json.RawMessage, string) error
	Payload   func(context.Context, platform.Actor, Event) (map[string]any, error)
}

// Config bounds each socket. Inbound frames spend one token from a bucket that
// refills once per FrameInterval up to FrameBurst, and typing is accepted at
// most once per TypingInterval; see frameLimiter.
type Config struct {
	OriginPatterns                             []string
	ReadLimit                                  int64
	WriteTimeout, IdleTimeout, RecheckInterval time.Duration
	FrameInterval, TypingInterval              time.Duration
	FrameBurst                                 int
}

func DefaultConfig() Config {
	return Config{ReadLimit: 2 << 20, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Minute, RecheckInterval: 30 * time.Second, FrameInterval: 200 * time.Millisecond, FrameBurst: 10, TypingInterval: typingWindow}
}

type Server struct {
	broker    *Broker
	authority Authority
	adapters  map[string]Adapter
	cfg       Config
	Now       func() time.Time
}

func NewServer(b *Broker, a Authority, adapters map[string]Adapter, c Config) (*Server, error) {
	if b == nil || a == nil || c.ReadLimit <= 0 || c.ReadLimit > 2<<20 || c.WriteTimeout <= 0 || c.WriteTimeout > 30*time.Second || c.IdleTimeout <= 0 || c.IdleTimeout > time.Hour || c.RecheckInterval < time.Second || c.RecheckInterval > time.Minute || c.FrameInterval <= 0 || c.FrameInterval > time.Minute || c.FrameBurst < 1 || c.FrameBurst > 1024 || c.TypingInterval <= 0 || c.TypingInterval > time.Minute {
		return nil, ErrUnavailable
	}
	for _, pattern := range c.OriginPatterns {
		if strings.ContainsAny(pattern, "*?[") || pattern == "" {
			return nil, ErrUnavailable
		}
	}
	for _, kind := range []string{"chat", "messaging"} {
		adapter, ok := adapters[kind]
		if !ok || adapter.Authorize == nil || adapter.Receive == nil || adapter.Payload == nil {
			return nil, ErrUnavailable
		}
	}
	return &Server{broker: b, authority: a, adapters: adapters, cfg: c, Now: time.Now}, nil
}
func (s *Server) Register(mux *http.ServeMux) {
	for _, kind := range []string{"chat", "messaging"} {
		mux.HandleFunc(exactRoute("GET /ws/"+kind+"/{room}/"), s.handler(kind))
	}
}
func (s *Server) authorized(ctx context.Context, r *http.Request, kind string, id int64) (platform.Actor, error) {
	a, e := s.authority(ctx, r.WithContext(ctx))
	if e != nil || !a.IsActive || a.ID <= 0 {
		return platform.Actor{}, platform.ErrForbidden
	}
	ok, e := s.adapters[kind].Authorize(ctx, a, id)
	if e != nil {
		return platform.Actor{}, e
	}
	if !ok {
		return platform.Actor{}, platform.ErrForbidden
	}
	return a, nil
}
func (s *Server) handler(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("room"), 10, 64)
		if e != nil || id < 1 {
			platform.Error(w, 403, "Permission denied.")
			return
		}
		a, e := s.authorized(r.Context(), r, kind, id)
		if e != nil {
			platform.Error(w, 403, "Permission denied.")
			return
		}
		_ = a
		// Cookie-authenticated browsers supply Origin; non-browser API clients may
		// omit it only while presenting an explicit Authorization credential.
		if r.Header.Get("Origin") == "" && r.Header.Get("Authorization") == "" {
			platform.Error(w, 403, "Origin required.")
			return
		}
		sub, e := s.broker.subscribe(kind, id)
		if e != nil {
			platform.Error(w, 503, "Live delivery unavailable.")
			return
		}
		defer s.broker.unsubscribe(sub)
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.cfg.OriginPatterns, CompressionMode: websocket.CompressionDisabled})
		if e != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(s.cfg.ReadLimit)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		var nonce [16]byte
		if _, e = rand.Read(nonce[:]); e != nil {
			return
		}
		sender := hex.EncodeToString(nonce[:])
		var writes sync.Mutex
		write := func(value any) error {
			raw, e := json.Marshal(value)
			if e != nil || int64(len(raw)) > s.cfg.ReadLimit {
				return ErrUnavailable
			}
			sendCtx, done := context.WithTimeout(ctx, s.cfg.WriteTimeout)
			defer done()
			writes.Lock()
			defer writes.Unlock()
			return conn.Write(sendCtx, websocket.MessageText, raw)
		}
		closePermission := func() { _ = conn.Close(websocket.StatusCode(4403), "Permission revoked."); cancel() }
		limiter := newFrameLimiter(s.cfg, s.Now)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				readCtx, done := context.WithTimeout(ctx, s.cfg.IdleTimeout)
				typ, body, e := conn.Read(readCtx)
				done()
				if e != nil {
					cancel()
					return
				}
				if typ != websocket.MessageText {
					_ = conn.Close(websocket.StatusUnsupportedData, "JSON text required.")
					cancel()
					return
				}
				// Metering precedes authorization: a dropped frame costs no
				// database work, and excess typing never closes the socket.
				switch limiter.allow(typingFrame(body)) {
				case frameDrop:
					continue
				case frameAbuse:
					_ = conn.Close(websocket.StatusPolicyViolation, "Too many messages.")
					cancel()
					return
				}
				actor, e := s.authorized(ctx, r, kind, id)
				if e != nil {
					closePermission()
					return
				}
				if e = s.adapters[kind].Receive(ctx, actor, id, body, sender); e != nil {
					if errors.Is(e, platform.ErrForbidden) {
						if _, e = s.authorized(ctx, r, kind, id); e != nil {
							closePermission()
							return
						}
					}
					if write(map[string]any{"type": "error", "detail": "Message could not be sent."}) != nil {
						cancel()
						return
					}
				}
			}
		}()
		ticker := time.NewTicker(s.cfg.RecheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-readDone:
				return
			case <-sub.done:
				_ = conn.Close(websocket.StatusServiceRestart, "Reload durable history.")
				cancel()
				return
			case <-ticker.C:
				if _, e = s.authorized(ctx, r, kind, id); e != nil {
					closePermission()
					return
				}
			case event := <-sub.events:
				if transient(event) && event.Sender == sender {
					continue
				}
				actor, e := s.authorized(ctx, r, kind, id)
				if e != nil {
					closePermission()
					return
				}
				payload, e := s.adapters[kind].Payload(ctx, actor, event)
				if e != nil {
					if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, platform.ErrNotFound) {
						continue
					}
					if errors.Is(e, platform.ErrForbidden) {
						closePermission()
						return
					}
					continue
				}
				if payload == nil {
					continue
				}
				if e = write(payload); e != nil {
					cancel()
					return
				}
			}
		}
	}
}

func exactRoute(pattern string) string { return strings.TrimSuffix(pattern, "/") + "/{$}" }
