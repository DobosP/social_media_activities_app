package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

var seedCopy = regexp.MustCompile(`^COPY public\.(places_place|places_placeactivity|events_event) \(([a-z_, ]+)\) FROM stdin;$`)
var seedSetval = regexp.MustCompile(`^SELECT pg_catalog\.setval\('public\.(places_place|places_placeactivity|events_event)_id_seq', ([0-9]+), true\);$`)

func (s *Service) demoGate() error {
	if !s.Config.DemoEnabled {
		return fmt.Errorf("%w: development-only command is disabled", platform.ErrForbidden)
	}
	return nil
}
func (s *Service) loadSeed(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	if err := s.demoGate(); err != nil {
		return nil, err
	}
	var in struct{ Path string }
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if s.Config.SeedRoot == "" {
		return nil, missingDependency("development seed root")
	}
	if in.Path == "" {
		in.Path = "seed-data.sql"
	}
	if filepath.IsAbs(in.Path) {
		rel, err := filepath.Rel(s.Config.SeedRoot, in.Path)
		if err != nil {
			return nil, platform.ErrInvalid
		}
		in.Path = rel
	}
	if in.Path == ".." || strings.HasPrefix(in.Path, "../") {
		return nil, platform.ErrForbidden
	}
	root, err := os.OpenRoot(s.Config.SeedRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(in.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, platform.ErrInvalid
	}
	counts := map[string]int64{}
	err = platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('native-development-seed',0))`); err != nil {
			return err
		}
		var seeded, occupied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place WHERE source='roedu') OR EXISTS(SELECT 1 FROM places_placeactivity WHERE source='roedu') OR EXISTS(SELECT 1 FROM events_event WHERE source='roedu')`).Scan(&seeded); err != nil {
			return err
		}
		if seeded {
			counts["already_present"] = 1
			return nil
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place) OR EXISTS(SELECT 1 FROM places_placeactivity) OR EXISTS(SELECT 1 FROM events_event)`).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			counts["non_seed_data_present"] = 1
			return nil
		}
		scanner := bufio.NewScanner(io.LimitReader(f, 64<<20+1))
		scanner.Buffer(make([]byte, 4096), 8<<20)
		index := 0
		for scanner.Scan() {
			line := scanner.Text()
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "--") {
				continue
			}
			if strings.HasPrefix(line, "SET ") {
				switch trim {
				case "SET client_encoding = 'UTF8';", "SET standard_conforming_strings = on;", "SET check_function_bodies = false;", "SET client_min_messages = warning;", "SET row_security = off;":
					continue
				default:
					return platform.ErrInvalid
				}
			}
			if seedSetval.MatchString(trim) {
				continue
			}
			match := seedCopy.FindStringSubmatch(trim)
			if match == nil {
				return fmt.Errorf("%w: unsupported data-only seed statement", platform.ErrInvalid)
			}
			table, fields := match[1], strings.Split(strings.ReplaceAll(match[2], " ", ""), ",")
			columns := []string{}
			for _, field := range fields {
				columns = append(columns, pgx.Identifier{field}.Sanitize())
			}
			stage := fmt.Sprintf("native_seed_stage_%d", index)
			index++
			if _, err := tx.Exec(ctx, `CREATE TEMP TABLE `+stage+` ON COMMIT DROP AS SELECT `+strings.Join(columns, ",")+` FROM `+pgx.Identifier{table}.Sanitize()+` WITH NO DATA`); err != nil {
				return err
			}
			var body strings.Builder
			ended := false
			for scanner.Scan() {
				line = scanner.Text()
				if line == `\.` {
					ended = true
					break
				}
				body.WriteString(line)
				body.WriteByte('\n')
				if body.Len() > 64<<20 {
					return platform.ErrInvalid
				}
			}
			if !ended {
				return fmt.Errorf("%w: unterminated COPY block", platform.ErrInvalid)
			}
			if _, err := tx.Conn().PgConn().CopyFrom(ctx, strings.NewReader(body.String()), `COPY `+stage+` (`+strings.Join(columns, ",")+`) FROM stdin`); err != nil {
				return err
			}
			projection := append([]string{}, columns...)
			if table == "events_event" {
				defaults := map[string]string{"source_category": "''", "source_city": "''", "source_pack_id": "''", "source_release_id": "''", "source_snapshot_id": "''", "source_venue_id": "''", "source_availability": "''", "source_currency": "''", "source_recurrence": "''", "source_timezone": "''", "is_import_held": "false", "is_tombstone": "false", "lifecycle_status": "'scheduled'"}
				for field, value := range defaults {
					present := false
					for _, existing := range fields {
						present = present || existing == field
					}
					if !present {
						columns = append(columns, pgx.Identifier{field}.Sanitize())
						projection = append(projection, value)
					}
				}
			}
			tag, err := tx.Exec(ctx, `INSERT INTO `+pgx.Identifier{table}.Sanitize()+` (`+strings.Join(columns, ",")+`) SELECT `+strings.Join(projection, ",")+` FROM `+stage)
			if err != nil {
				return err
			}
			counts[table] = tag.RowsAffected()
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		for _, table := range []string{"places_place", "places_placeactivity", "events_event"} {
			var maximum int64
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(id),0) FROM `+pgx.Identifier{table}.Sanitize()).Scan(&maximum); err != nil {
				return err
			}
			var last int64
			if err := tx.QueryRow(ctx, `SELECT last_value FROM `+pgx.Identifier{"public", table + "_id_seq"}.Sanitize()).Scan(&last); err != nil {
				return err
			}
			if maximum > last {
				if _, err := tx.Exec(ctx, `SELECT pg_catalog.setval($1::regclass,$2,true)`, "public."+table+"_id_seq", maximum); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return counts, err
}
