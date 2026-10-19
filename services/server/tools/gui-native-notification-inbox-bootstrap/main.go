// Command gui-native-notification-inbox-bootstrap supplies the same canonical
// catalog migration to the notifications-only isolated qualification database.
// It never serves HTTP, discovers credentials or inserts test-only reference rows.
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

var fixtureDSN = regexp.MustCompile(`^host=/pgsocket port=55434 user=app dbname=social_family_[0-9a-f]{12}_notifications sslmode=disable$`)

// qualificationDSN is pure: valid-grammar controls never open a database.
func qualificationDSN(args []string) (string, error) {
	flags := flag.NewFlagSet("gui-native-notification-inbox-bootstrap", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dsn := flags.String("dsn", "", "explicit isolated qualification Unix-socket DSN")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !fixtureDSN.MatchString(*dsn) {
		return "", errors.New("explicit isolated qualification DSN required")
	}
	return *dsn, nil
}

func run(args []string, output io.Writer) error {
	dsn, err := qualificationDSN(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
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
