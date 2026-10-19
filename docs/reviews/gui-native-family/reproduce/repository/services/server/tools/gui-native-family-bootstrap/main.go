// Command gui-native-family-bootstrap supplies the same complete reference data
// migration used by social-server --migrate-only to isolated qualification DBs.
// It never serves HTTP, discovers service credentials or inserts test-only rows.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

var fixtureDSN = regexp.MustCompile(`^host=/pgsocket port=55434 user=app dbname=social_family_[0-9a-f]{12}_(web|app|accounts|recommendations|social|messaging) sslmode=disable$`)

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("gui-native-family-bootstrap", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dsn := flags.String("dsn", "", "explicit isolated qualification Unix-socket DSN")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !fixtureDSN.MatchString(*dsn) {
		return errors.New("explicit isolated qualification DSN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return errors.New("qualification database unavailable")
	}
	defer db.Close()
	if err := catalog.New(db).Migrate(ctx); err != nil {
		return errors.New("canonical catalog migration failed")
	}
	return json.NewEncoder(output).Encode(map[string]string{"status": "catalog-migrated"})
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
