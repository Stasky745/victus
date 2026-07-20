// Package goals is the business-logic layer for Victus's Configuration
// tab: per-user nutrient goal ranges (min/max) and the app-wide "how to set
// healthy targets" info link. Both retrofit the Day and Weekly Builder
// summaries with range-aware coloring once a user has configured them.
package goals

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
)

// goalInfoURLKey is the app_settings row seeded (with a sensible default)
// by migration 00005 — the Configuration tab lets a user override it.
const goalInfoURLKey = "goal_info_url"

// Status describes how a nutrient's current total compares to its
// configured goal range.
type Status int

const (
	// StatusNoGoal means the user hasn't configured any bound for this
	// nutrient — there's nothing to compare against.
	StatusNoGoal Status = iota
	// StatusUnder means total is below the configured (hard) minimum.
	StatusUnder
	// StatusOver means total is above the configured (hard) maximum.
	StatusOver
	// StatusAcceptable means total satisfies min/max but falls outside the
	// configured ideal sub-range (e.g. within the hard floor/ceiling but
	// below IdealMin or above IdealMax) — "fine, but not the sweet spot."
	StatusAcceptable
	// StatusIdeal means total is within the configured ideal range, or, if
	// no ideal range is configured at all, simply within min/max — the
	// original (pre-ideal-range) meaning of "in range."
	StatusIdeal
)

// Goal is one nutrient's configured range for a user. Every bound is
// independently optional: MinValue/MaxValue are the hard floor/ceiling,
// IdealMin/IdealMax are an optional narrower "sweet spot" inside them (e.g.
// sugar might have no minimum, an ideal max of 30g, and a hard max of
// 50g — IdealMin stays nil). A goal with every bound nil is equivalent to
// no goal at all.
type Goal struct {
	NutrientID  int16
	Key         string
	DisplayName string
	Unit        string
	MinValue    *float64
	MaxValue    *float64
	IdealMin    *float64
	IdealMax    *float64
}

// Status compares total against g's configured bounds: first the hard
// min/max (violating either is always Under/Over, regardless of ideal
// bounds), then the optional ideal sub-range.
func (g Goal) Status(total float64) Status {
	switch {
	case g.MinValue == nil && g.MaxValue == nil && g.IdealMin == nil && g.IdealMax == nil:
		return StatusNoGoal
	case g.MinValue != nil && total < *g.MinValue:
		return StatusUnder
	case g.MaxValue != nil && total > *g.MaxValue:
		return StatusOver
	case g.IdealMin != nil && total < *g.IdealMin:
		return StatusAcceptable
	case g.IdealMax != nil && total > *g.IdealMax:
		return StatusAcceptable
	default:
		return StatusIdeal
	}
}

// Lookup finds nutrientID's goal in goalsList, or a zero-value Goal (no
// bounds configured) if none exists — callers can call .Status(total) on
// the result either way, without a separate "not found" branch.
func Lookup(goalsList []Goal, nutrientID int16) Goal {
	for _, g := range goalsList {
		if g.NutrientID == nutrientID {
			return g
		}
	}
	return Goal{NutrientID: nutrientID}
}

// GoalInput is one nutrient's submitted range, for SaveGoals.
type GoalInput struct {
	NutrientID int16
	MinValue   *float64
	MaxValue   *float64
	IdealMin   *float64
	IdealMax   *float64
}

// Store is the goals/settings repository.
type Store struct {
	sqlDB  *sql.DB
	driver string
	q      sqlc.Querier
}

// New returns a Store backed by sqlDB for driver ("postgres" or "sqlite").
func New(sqlDB *sql.DB, driver string) (*Store, error) {
	q, err := db.NewQuerier(driver, sqlDB)
	if err != nil {
		return nil, err
	}
	return &Store{sqlDB: sqlDB, driver: driver, q: q}, nil
}

// ListGoals returns every known nutrient (in display order) with userID's
// configured range, if any — every nutrient is always present (mirroring
// planning.blankTotals) so the Configuration form has a stable set of rows
// even before the user has set anything.
func (s *Store) ListGoals(ctx context.Context, userID uuid.UUID) ([]Goal, error) {
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nutrients: %w", err)
	}
	rows, err := s.q.GetUserNutrientGoals(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user nutrient goals: %w", err)
	}
	byNutrientID := make(map[int16]sqlc.GetUserNutrientGoalsRow, len(rows))
	for _, row := range rows {
		byNutrientID[row.NutrientID] = row
	}

	goalsList := make([]Goal, len(nutrients))
	for i, n := range nutrients {
		g := Goal{NutrientID: n.ID, Key: n.Key, DisplayName: n.DisplayName, Unit: n.Unit}
		if row, ok := byNutrientID[n.ID]; ok {
			g.MinValue = row.MinValue
			g.MaxValue = row.MaxValue
			g.IdealMin = row.IdealMin
			g.IdealMax = row.IdealMax
		}
		goalsList[i] = g
	}
	return goalsList, nil
}

// InfoURL returns the current "how to set healthy targets" link.
func (s *Store) InfoURL(ctx context.Context) (string, error) {
	val, err := s.q.GetAppSetting(ctx, goalInfoURLKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil // seeded by migration; empty only if removed out-of-band
		}
		return "", fmt.Errorf("get goal info url: %w", err)
	}
	return val, nil
}

// SaveGoals atomically updates every submitted per-user goal range in one
// transaction — so a mid-save failure (e.g. one bad value deep in the
// nutrient list) never leaves some ranges updated and others stale.
//
// Deliberately separate from SetInfoURL (not bundled into one save call
// spanning both a per-user write and the instance-wide info URL): the two
// used to share one form/one transaction, which meant every ordinary
// personal-goal save also re-wrote the shared info URL with whatever value
// happened to be sitting in that user's already-rendered form — silently
// clobbering a more recent change another user made in the meantime, even
// though they never intended to touch it. Keeping them as separate saves
// (separate forms in the UI) means saving your own goals can never
// accidentally overwrite the shared link, and vice versa.
func (s *Store) SaveGoals(ctx context.Context, userID uuid.UUID, inputs []GoalInput) error {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q, err := db.NewTxQuerier(s.driver, tx)
	if err != nil {
		return err
	}

	for _, in := range inputs {
		if err := q.SetUserNutrientGoal(ctx, sqlc.SetUserNutrientGoalParams{
			UserID:     userID,
			NutrientID: in.NutrientID,
			MinValue:   in.MinValue,
			MaxValue:   in.MaxValue,
			IdealMin:   in.IdealMin,
			IdealMax:   in.IdealMax,
		}); err != nil {
			return fmt.Errorf("set goal for nutrient %d: %w", in.NutrientID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// SetInfoURL overrides the instance-wide "how to set healthy targets" link.
// See SaveGoals's doc comment for why this is a separate call rather than
// bundled into the same save as a user's personal goal ranges.
func (s *Store) SetInfoURL(ctx context.Context, url string) error {
	if err := s.q.SetAppSetting(ctx, sqlc.SetAppSettingParams{Key: goalInfoURLKey, Value: url}); err != nil {
		return fmt.Errorf("set goal info url: %w", err)
	}
	return nil
}
