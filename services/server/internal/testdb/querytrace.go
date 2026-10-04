package testdb

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryCounter records counts only: no SQL, arguments, account data or credentials.
// Serving packages must never import testdb. A separate pool gives every fixture
// connection the tracer, including queries reached through domain adapters.
type QueryCounter struct{ queries atomic.Int64 }

func (c *QueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.queries.Add(1)
	return ctx
}
func (*QueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (c *QueryCounter) Reset()                                                        { c.queries.Store(0) }
func (c *QueryCounter) Count() int64                                                  { return c.queries.Load() }

func TracedPool(t testing.TB, db *pgxpool.Pool) (*pgxpool.Pool, *QueryCounter) {
	t.Helper()
	config := db.Config().Copy()
	trace := &QueryCounter{}
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal("query-growth fixture pool unavailable")
	}
	t.Cleanup(pool.Close)
	return pool, trace
}
