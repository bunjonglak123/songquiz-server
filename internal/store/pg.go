package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type PG struct{ pool *pgxpool.Pool }

// NewPG connects and applies pending migrations. Uses the simple protocol so it
// also works through Supabase's transaction pooler (no prepared statements).
func NewPG(ctx context.Context, url string) (*PG, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &PG{pool: pool}, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name())
	}
	sort.Strings(names)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// One migrator at a time, even with several instances starting together.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(727274)"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	for _, name := range names {
		var done bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PG) SaveGame(ctx context.Context, r GameRecord) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO games (id, room_code, source_kind, rounds, player_count, started_at, ended_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.ID, r.RoomCode, r.SourceKind, r.Rounds, len(r.Players), r.StartedAt, r.EndedAt); err != nil {
		return err
	}
	for _, p := range r.Players {
		if _, err := tx.Exec(ctx,
			`INSERT INTO game_players (game_id, player_id, name, score, rank, correct) VALUES ($1, $2, $3, $4, $5, $6)`,
			r.ID, p.PlayerID, p.Name, p.Score, p.Rank, p.Correct); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PG) Top(ctx context.Context, period string, limit int, now time.Time) ([]Entry, error) {
	from, err := since(period, now)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT gp.name, gp.score, gp.correct, g.rounds, g.ended_at
		   FROM game_players gp JOIN games g ON g.id = gp.game_id
		  WHERE g.player_count >= $1 AND g.rounds >= $2 AND g.ended_at >= $3
		  ORDER BY gp.score DESC, g.ended_at ASC
		  LIMIT $4`,
		MinPlayers, MinRounds, from, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Entry, error) {
		var e Entry
		err := r.Scan(&e.Name, &e.Score, &e.Correct, &e.Rounds, &e.PlayedAt)
		return e, err
	})
	if out == nil {
		out = []Entry{}
	}
	return out, err
}

func (s *PG) Close() { s.pool.Close() }
