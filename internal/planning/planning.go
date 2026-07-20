// Package planning is the business-logic layer for Victus's Day Builder:
// reading a user's day (grouped by meal category, with nutrient totals) and
// adding/removing/adjusting the meals in it. Every mutation is followed by a
// fresh read, so the HTTP layer can render an always-consistent htmx summary
// without tracking anything client-side.
package planning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
)

// ErrNotOwner is returned when a user attempts to modify a day-plan item
// that belongs to someone else's day plan.
var ErrNotOwner = errors.New("not the owner of this day plan item")

// ErrItemNotFound is returned when a day-plan item id doesn't exist at all
// (already deleted, or never existed) — distinct from ErrNotOwner so
// callers can treat both as "not found" without leaking which case it was.
var ErrItemNotFound = errors.New("day plan item not found")

// Store is the day-planning repository.
type Store struct {
	db     *sql.DB
	driver string
	q      sqlc.Querier
}

// New returns a Store backed by sqlDB for driver ("postgres" or "sqlite").
func New(sqlDB *sql.DB, driver string) (*Store, error) {
	q, err := db.NewQuerier(driver, sqlDB)
	if err != nil {
		return nil, err
	}
	return &Store{db: sqlDB, driver: driver, q: q}, nil
}

// withTx runs fn inside one transaction: begin, fn(a Querier bound to the
// tx), commit — with the tx always rolled back on any early return.
func (s *Store) withTx(ctx context.Context, fn func(q sqlc.Querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q, err := db.NewTxQuerier(s.driver, tx)
	if err != nil {
		return err
	}
	if err := fn(q); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Item is one meal placed into a day plan's category slot.
type Item struct {
	ID         uuid.UUID
	MealID     uuid.UUID
	MealName   string
	CategoryID uuid.UUID
	Quantity   float64
}

// CategorySection groups a day's items under one meal category, in display order.
type CategorySection struct {
	Category sqlc.MealCategory
	Items    []Item
}

// NutrientTotal is one nutrient's running total for a day, across every
// item's quantity-scaled contribution. Every known nutrient is always
// present (Total 0 if nothing tracks it yet), so templates can render a
// stable set of rows.
type NutrientTotal struct {
	NutrientID  int16
	Key         string
	DisplayName string
	Unit        string
	Total       float64
}

// Day is a single date's full Day Builder view for one user.
type Day struct {
	Date       time.Time
	PlanID     uuid.UUID // uuid.Nil if the user has no plan for this date yet
	Categories []CategorySection
	Totals     []NutrientTotal
}

// GetDay loads the full Day Builder view for userID on date. A user with no
// day plan yet for that date gets an all-empty Day (every category present,
// no items, every nutrient total 0) rather than an error — there's nothing
// wrong with an empty day.
func (s *Store) GetDay(ctx context.Context, userID uuid.UUID, date time.Time) (Day, error) {
	categories, err := s.q.ListMealCategories(ctx)
	if err != nil {
		return Day{}, fmt.Errorf("list categories: %w", err)
	}
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return Day{}, fmt.Errorf("list nutrients: %w", err)
	}
	return s.getDay(ctx, userID, date, categories, nutrients)
}

// getDay is GetDay's implementation, taking the meal-category and nutrient
// registries as parameters instead of fetching them itself — both are
// near-static reference tables, so GetWeek fetches them once and passes them
// to every one of its 7 getDay calls rather than re-querying the same rows
// 7 times over.
func (s *Store) getDay(ctx context.Context, userID uuid.UUID, date time.Time, categories []sqlc.MealCategory, nutrients []sqlc.Nutrient) (Day, error) {
	sections := make([]CategorySection, len(categories))
	byCategoryID := make(map[uuid.UUID]int, len(categories))
	for i, c := range categories {
		sections[i] = CategorySection{Category: c}
		byCategoryID[c.ID] = i
	}

	totals := blankTotals(nutrients)

	dayPlan, err := s.q.GetDayPlan(ctx, sqlc.GetDayPlanParams{
		UserID:   userID,
		PlanDate: date,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// A day the user has never touched — apply their configured
		// defaults (if any), so it behaves from now on like a normal,
		// editable day. Deliberately not materialized when there are no
		// defaults configured: that keeps the common case (no defaults set
		// up) a pure read, exactly as before this feature existed.
		defaultItems, derr := s.q.ListDefaultDayItems(ctx, userID)
		if derr != nil {
			return Day{}, fmt.Errorf("list default day items: %w", derr)
		}
		if len(defaultItems) == 0 {
			return Day{Date: date, Categories: sections, Totals: totals}, nil
		}
		return s.materializeDefaults(ctx, userID, date, defaultItems, sections, byCategoryID, totals)
	case err != nil:
		return Day{}, fmt.Errorf("get day plan: %w", err)
	}

	items, err := s.q.ListDayPlanItems(ctx, dayPlan.ID)
	if err != nil {
		return Day{}, fmt.Errorf("list day plan items: %w", err)
	}
	for _, row := range items {
		idx, ok := byCategoryID[row.CategoryID]
		if !ok {
			continue // category was deleted after this item was added; skip rather than panic
		}
		sections[idx].Items = append(sections[idx].Items, Item{
			ID:         row.ID,
			MealID:     row.MealID,
			MealName:   row.MealName,
			CategoryID: row.CategoryID,
			Quantity:   row.Quantity,
		})
	}

	totalRows, err := s.q.GetDayPlanTotals(ctx, dayPlan.ID)
	if err != nil {
		return Day{}, fmt.Errorf("get day plan totals: %w", err)
	}
	applyTotals(totals, totalRows)

	return Day{
		Date:       date,
		PlanID:     dayPlan.ID,
		Categories: sections,
		Totals:     totals,
	}, nil
}

// materializeDefaults copies userID's configured default items into a
// freshly-created day plan for date, inside one transaction — either the
// whole default set lands, or none of it does, so a partial failure never
// leaves a half-applied day plan for the caller to stumble on later.
func (s *Store) materializeDefaults(
	ctx context.Context,
	userID uuid.UUID,
	date time.Time,
	defaultItems []sqlc.ListDefaultDayItemsRow,
	sections []CategorySection,
	byCategoryID map[uuid.UUID]int,
	totals []NutrientTotal,
) (Day, error) {
	var planID uuid.UUID
	err := s.withTx(ctx, func(q sqlc.Querier) error {
		dayPlan, err := q.GetOrCreateDayPlan(ctx, sqlc.GetOrCreateDayPlanParams{
			ID:       uuid.New(),
			UserID:   userID,
			PlanDate: date,
		})
		if err != nil {
			return fmt.Errorf("materialize day plan: %w", err)
		}
		planID = dayPlan.ID

		for _, def := range defaultItems {
			idx, ok := byCategoryID[def.CategoryID]
			if !ok {
				continue // category was deleted after this default was configured
			}
			row, err := q.AddDayPlanItem(ctx, sqlc.AddDayPlanItemParams{
				ID:         uuid.New(),
				DayPlanID:  dayPlan.ID,
				CategoryID: def.CategoryID,
				MealID:     def.MealID,
				Quantity:   def.Quantity,
			})
			if err != nil {
				return fmt.Errorf("materialize default item: %w", err)
			}
			sections[idx].Items = append(sections[idx].Items, Item{
				ID:         row.ID,
				MealID:     row.MealID,
				MealName:   def.MealName,
				CategoryID: row.CategoryID,
				Quantity:   row.Quantity,
			})
		}

		totalRows, err := q.GetDayPlanTotals(ctx, dayPlan.ID)
		if err != nil {
			return fmt.Errorf("get materialized day plan totals: %w", err)
		}
		applyTotals(totals, totalRows)
		return nil
	})
	if err != nil {
		return Day{}, err
	}
	return Day{
		Date:       date,
		PlanID:     planID,
		Categories: sections,
		Totals:     totals,
	}, nil
}

// applyTotals fills in totals (every known nutrient, pre-populated at 0 by
// blankTotals) with the actual sums from totalRows, in place.
func applyTotals(totals []NutrientTotal, totalRows []sqlc.GetDayPlanTotalsRow) {
	byNutrientID := make(map[int16]float64, len(totalRows))
	for _, row := range totalRows {
		byNutrientID[row.NutrientID] = row.Total
	}
	for i := range totals {
		if v, ok := byNutrientID[totals[i].NutrientID]; ok {
			totals[i].Total = v
		}
	}
}

// WeekLength is the number of days in a week view (Monday through Sunday).
const WeekLength = 7

// Week is a Monday-anchored 7-day view for one user. Deliberately not
// backed by any week-level row of its own — a "week" is just 7 existing
// Days (day_plans are queried directly), so it can't drift from what the
// Day Builder shows for the same dates.
type Week struct {
	Start   time.Time // Monday
	Days    []Day     // always WeekLength entries, Monday..Sunday
	Average []NutrientTotal
}

// GetWeek loads a Monday-anchored week's worth of days for userID, reusing
// GetDay for each one so the Day Builder and Weekly Builder always agree on
// what a "day" looks like.
func (s *Store) GetWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) (Week, error) {
	categories, err := s.q.ListMealCategories(ctx)
	if err != nil {
		return Week{}, fmt.Errorf("list categories: %w", err)
	}
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return Week{}, fmt.Errorf("list nutrients: %w", err)
	}

	days := make([]Day, WeekLength)
	for i := range WeekLength {
		d, err := s.getDay(ctx, userID, weekStart.AddDate(0, 0, i), categories, nutrients)
		if err != nil {
			return Week{}, fmt.Errorf("get day %d of week: %w", i, err)
		}
		days[i] = d
	}

	// Keyed by NutrientID rather than positional slice index: every day's
	// Totals is built from the same ListNutrients query so the order always
	// matches today, but summing by identity rather than position means a
	// future per-day nutrient subset (or reordering) can't silently misalign
	// one day's protein total onto another day's calorie slot.
	average := make([]NutrientTotal, len(days[0].Totals))
	for i, nt := range days[0].Totals {
		average[i] = NutrientTotal{NutrientID: nt.NutrientID, Key: nt.Key, DisplayName: nt.DisplayName, Unit: nt.Unit}
	}
	byNutrientID := make(map[int16]*NutrientTotal, len(average))
	for i := range average {
		byNutrientID[average[i].NutrientID] = &average[i]
	}
	for _, d := range days {
		for _, nt := range d.Totals {
			if avg, ok := byNutrientID[nt.NutrientID]; ok {
				avg.Total += nt.Total
			}
		}
	}
	for i := range average {
		average[i].Total /= float64(WeekLength)
	}

	return Week{Start: weekStart, Days: days, Average: average}, nil
}

// CopyDay copies every item from userID's sourceDate into each of
// targetDates, additively — existing items already on a target day are
// left alone, not replaced (copying is meant to save re-entry, not to be a
// destructive overwrite the user has to be careful with). All copies for
// all target dates happen in one transaction: either everything lands, or
// nothing does.
//
// targetDates is deduplicated and any entry equal to sourceDate is dropped
// before copying — the HTTP layer's checkboxes can't produce either case,
// but a resubmitted/forged request could, and without this a repeated or
// self-targeting date would silently double every item.
func (s *Store) CopyDay(ctx context.Context, userID uuid.UUID, sourceDate time.Time, targetDates []time.Time) error {
	const dateKeyLayout = "2006-01-02"
	sourceKey := sourceDate.Format(dateKeyLayout)
	seen := make(map[string]bool, len(targetDates))
	deduped := make([]time.Time, 0, len(targetDates))
	for _, target := range targetDates {
		key := target.Format(dateKeyLayout)
		if key == sourceKey || seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, target)
	}
	targetDates = deduped
	if len(targetDates) == 0 {
		return nil
	}

	source, err := s.GetDay(ctx, userID, sourceDate)
	if err != nil {
		return fmt.Errorf("get source day: %w", err)
	}

	type copiedItem struct {
		categoryID uuid.UUID
		mealID     uuid.UUID
		quantity   float64
	}
	var items []copiedItem
	for _, section := range source.Categories {
		for _, item := range section.Items {
			items = append(items, copiedItem{
				categoryID: item.CategoryID,
				mealID:     item.MealID,
				quantity:   item.Quantity,
			})
		}
	}
	if len(items) == 0 {
		return nil
	}

	return s.withTx(ctx, func(q sqlc.Querier) error {
		for _, target := range targetDates {
			dayPlan, err := q.GetOrCreateDayPlan(ctx, sqlc.GetOrCreateDayPlanParams{
				ID:       uuid.New(),
				UserID:   userID,
				PlanDate: target,
			})
			if err != nil {
				return fmt.Errorf("get or create target day plan: %w", err)
			}
			for _, item := range items {
				if _, err := q.AddDayPlanItem(ctx, sqlc.AddDayPlanItemParams{
					ID:         uuid.New(),
					DayPlanID:  dayPlan.ID,
					CategoryID: item.categoryID,
					MealID:     item.mealID,
					Quantity:   item.quantity,
				}); err != nil {
					return fmt.Errorf("add copied item: %w", err)
				}
			}
		}
		return nil
	})
}

// AddItem places a meal into a category slot on the given date, creating
// the day plan itself on first use. Both writes happen in one transaction,
// so a failure adding the item (e.g. a bad category/meal id) never leaves
// behind an empty day plan row that the week view would later count as
// "planned".
func (s *Store) AddItem(ctx context.Context, userID uuid.UUID, date time.Time, categoryID, mealID uuid.UUID, quantity float64) error {
	return s.withTx(ctx, func(q sqlc.Querier) error {
		dayPlan, err := q.GetOrCreateDayPlan(ctx, sqlc.GetOrCreateDayPlanParams{
			ID:       uuid.New(),
			UserID:   userID,
			PlanDate: date,
		})
		if err != nil {
			return fmt.Errorf("get or create day plan: %w", err)
		}

		if _, err := q.AddDayPlanItem(ctx, sqlc.AddDayPlanItemParams{
			ID:         uuid.New(),
			DayPlanID:  dayPlan.ID,
			CategoryID: categoryID,
			MealID:     mealID,
			Quantity:   quantity,
		}); err != nil {
			return fmt.Errorf("add day plan item: %w", err)
		}
		return nil
	})
}

// UpdateItemQuantity changes an existing item's quantity, after confirming
// userID actually owns the day plan it belongs to.
func (s *Store) UpdateItemQuantity(ctx context.Context, userID, itemID uuid.UUID, quantity float64) error {
	if err := s.checkOwnership(ctx, userID, itemID); err != nil {
		return err
	}
	if err := s.q.UpdateDayPlanItemQuantity(ctx, sqlc.UpdateDayPlanItemQuantityParams{
		ID:       itemID,
		Quantity: quantity,
	}); err != nil {
		return fmt.Errorf("update item quantity: %w", err)
	}
	return nil
}

// RemoveItem deletes an item, after confirming userID actually owns the day
// plan it belongs to.
func (s *Store) RemoveItem(ctx context.Context, userID, itemID uuid.UUID) error {
	if err := s.checkOwnership(ctx, userID, itemID); err != nil {
		return err
	}
	if err := s.q.RemoveDayPlanItem(ctx, itemID); err != nil {
		return fmt.Errorf("remove day plan item: %w", err)
	}
	return nil
}

// GetDefaultDay loads userID's configured "Default Day" template — shaped
// exactly like a Day's Categories, so the Default Day page can reuse the Day
// Builder's CategoryItems rendering unchanged.
func (s *Store) GetDefaultDay(ctx context.Context, userID uuid.UUID) ([]CategorySection, error) {
	categories, err := s.q.ListMealCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	sections := make([]CategorySection, len(categories))
	byCategoryID := make(map[uuid.UUID]int, len(categories))
	for i, c := range categories {
		sections[i] = CategorySection{Category: c}
		byCategoryID[c.ID] = i
	}

	items, err := s.q.ListDefaultDayItems(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list default day items: %w", err)
	}
	for _, row := range items {
		idx, ok := byCategoryID[row.CategoryID]
		if !ok {
			continue // category was deleted after this default was configured
		}
		sections[idx].Items = append(sections[idx].Items, Item{
			ID:         row.ID,
			MealID:     row.MealID,
			MealName:   row.MealName,
			CategoryID: row.CategoryID,
			Quantity:   row.Quantity,
		})
	}
	return sections, nil
}

// AddDefaultItem adds a meal to userID's Default Day template — it never
// touches any already-materialized day plan, only future ones (see getDay's
// materialization).
func (s *Store) AddDefaultItem(ctx context.Context, userID uuid.UUID, categoryID, mealID uuid.UUID, quantity float64) error {
	if _, err := s.q.AddDefaultDayItem(ctx, sqlc.AddDefaultDayItemParams{
		ID:         uuid.New(),
		UserID:     userID,
		CategoryID: categoryID,
		MealID:     mealID,
		Quantity:   quantity,
	}); err != nil {
		return fmt.Errorf("add default day item: %w", err)
	}
	return nil
}

// RemoveDefaultItem deletes a Default Day item, scoped to userID so one
// user can never remove another's default — returns ErrItemNotFound if
// itemID doesn't exist or doesn't belong to userID (indistinguishable on
// purpose, same as RemoveItem's ownership check).
func (s *Store) RemoveDefaultItem(ctx context.Context, userID, itemID uuid.UUID) error {
	n, err := s.q.RemoveDefaultDayItem(ctx, sqlc.RemoveDefaultDayItemParams{
		ID:     itemID,
		UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("remove default day item: %w", err)
	}
	if n == 0 {
		return ErrItemNotFound
	}
	return nil
}

// checkOwnership returns ErrItemNotFound if itemID doesn't exist at all,
// or ErrNotOwner if it exists but belongs to a different user — callers
// typically treat both as a 404, but they're distinguished here so the
// server-side log can tell "already gone" apart from "wrong user".
func (s *Store) checkOwnership(ctx context.Context, userID, itemID uuid.UUID) error {
	owner, err := s.q.GetDayPlanItemOwner(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrItemNotFound
	}
	if err != nil {
		return fmt.Errorf("look up item owner: %w", err)
	}
	if owner.UserID != userID {
		return ErrNotOwner
	}
	return nil
}

func blankTotals(nutrients []sqlc.Nutrient) []NutrientTotal {
	out := make([]NutrientTotal, len(nutrients))
	for i, n := range nutrients {
		out[i] = NutrientTotal{NutrientID: n.ID, Key: n.Key, DisplayName: n.DisplayName, Unit: n.Unit}
	}
	return out
}
