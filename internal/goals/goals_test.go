package goals_test

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/goals"
)

// newTestStore returns a goals.Store and the raw Querier backing it (for
// test fixtures like newTestUser that need to reach the DB directly),
// against whichever backend TEST_DB_DRIVER selects.
func newTestStore(t *testing.T) (*goals.Store, sqlc.Querier) {
	t.Helper()
	sqlDB := dbtest.NewDB(t)
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	store, err := goals.New(sqlDB, dbtest.Driver())
	if err != nil {
		t.Fatalf("new goals store: %v", err)
	}
	return store, q
}

func newTestUser(t *testing.T, q sqlc.Querier, label string) uuid.UUID {
	t.Helper()
	user, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{
		ID:          uuid.New(),
		OidcSubject: sql.NullString{String: "test-subject-" + t.Name() + "-" + label, Valid: true},
		Email:       label + "@example.com",
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user.ID
}

func nutrientIDByKey(t *testing.T, goalsList []goals.Goal, key string) int16 {
	t.Helper()
	for _, g := range goalsList {
		if g.Key == key {
			return g.NutrientID
		}
	}
	t.Fatalf("nutrient %q not found in goals list", key)
	return 0
}

func TestStore_ListGoals_EmptyByDefault(t *testing.T) {
	store, q := newTestStore(t)
	userID := newTestUser(t, q, "a")

	goalsList, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	if len(goalsList) == 0 {
		t.Fatal("expected the seeded nutrient registry to still be listed even with no goals set")
	}
	for _, g := range goalsList {
		if g.MinValue != nil || g.MaxValue != nil {
			t.Errorf("nutrient %q: expected no bounds set, got min=%v max=%v", g.DisplayName, g.MinValue, g.MaxValue)
		}
		if g.Status(1000) != goals.StatusNoGoal {
			t.Errorf("nutrient %q: expected StatusNoGoal with no bounds configured", g.DisplayName)
		}
	}
}

func TestStore_SaveGoals_RoundTrips(t *testing.T) {
	store, q := newTestStore(t)
	userID := newTestUser(t, q, "a")

	before, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	calID := nutrientIDByKey(t, before, "calories")
	proteinID := nutrientIDByKey(t, before, "protein_g")

	calMin, calMax := 1300.0, 1600.0
	proteinMin := 80.0
	if err := store.SaveGoals(t.Context(), userID, []goals.GoalInput{
		{NutrientID: calID, MinValue: &calMin, MaxValue: &calMax},
		{NutrientID: proteinID, MinValue: &proteinMin, MaxValue: nil},
	}); err != nil {
		t.Fatalf("save goals: %v", err)
	}
	if err := store.SetInfoURL(t.Context(), "https://example.com/healthy-targets"); err != nil {
		t.Fatalf("set info url: %v", err)
	}

	after, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals after save: %v", err)
	}
	calGoal := goals.Lookup(after, calID)
	if calGoal.MinValue == nil || *calGoal.MinValue != calMin {
		t.Errorf("calories min = %v, want %v", calGoal.MinValue, calMin)
	}
	if calGoal.MaxValue == nil || *calGoal.MaxValue != calMax {
		t.Errorf("calories max = %v, want %v", calGoal.MaxValue, calMax)
	}
	if got := calGoal.Status(1450); got != goals.StatusIdeal {
		t.Errorf("calories status at 1450 = %v, want StatusIdeal", got)
	}
	if got := calGoal.Status(1000); got != goals.StatusUnder {
		t.Errorf("calories status at 1000 = %v, want StatusUnder", got)
	}
	if got := calGoal.Status(2000); got != goals.StatusOver {
		t.Errorf("calories status at 2000 = %v, want StatusOver", got)
	}

	proteinGoal := goals.Lookup(after, proteinID)
	if proteinGoal.MinValue == nil || *proteinGoal.MinValue != proteinMin {
		t.Errorf("protein min = %v, want %v", proteinGoal.MinValue, proteinMin)
	}
	if proteinGoal.MaxValue != nil {
		t.Errorf("protein max = %v, want nil (only a minimum was set)", *proteinGoal.MaxValue)
	}
	// No maximum configured — an arbitrarily high total should still be in range.
	if got := proteinGoal.Status(500); got != goals.StatusIdeal {
		t.Errorf("protein status at 500 (no max set) = %v, want StatusIdeal", got)
	}

	url, err := store.InfoURL(t.Context())
	if err != nil {
		t.Fatalf("get info url: %v", err)
	}
	if url != "https://example.com/healthy-targets" {
		t.Errorf("info url = %q, want the saved override", url)
	}
}

// TestStore_SaveGoals_IdealRangeRoundTrips guards the ideal_min/ideal_max
// columns specifically — SaveGoals/ListGoals must persist and reload them
// the same way min_value/max_value already do, including a one-sided ideal
// bound (only IdealMax set, matching sugar's shape: no minimum, an ideal
// ceiling, and a hard ceiling).
func TestStore_SaveGoals_IdealRangeRoundTrips(t *testing.T) {
	store, q := newTestStore(t)
	userID := newTestUser(t, q, "a")

	before, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	proteinID := nutrientIDByKey(t, before, "protein_g")
	sugarID := nutrientIDByKey(t, before, "sugar_g")

	proteinMin, proteinMax := 100.0, 150.0
	proteinIdealMin, proteinIdealMax := 110.0, 130.0
	sugarMax, sugarIdealMax := 50.0, 30.0
	if err := store.SaveGoals(t.Context(), userID, []goals.GoalInput{
		{NutrientID: proteinID, MinValue: &proteinMin, MaxValue: &proteinMax, IdealMin: &proteinIdealMin, IdealMax: &proteinIdealMax},
		{NutrientID: sugarID, MaxValue: &sugarMax, IdealMax: &sugarIdealMax},
	}); err != nil {
		t.Fatalf("save goals: %v", err)
	}

	after, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals after save: %v", err)
	}

	proteinGoal := goals.Lookup(after, proteinID)
	if proteinGoal.IdealMin == nil || *proteinGoal.IdealMin != proteinIdealMin {
		t.Errorf("protein ideal min = %v, want %v", proteinGoal.IdealMin, proteinIdealMin)
	}
	if proteinGoal.IdealMax == nil || *proteinGoal.IdealMax != proteinIdealMax {
		t.Errorf("protein ideal max = %v, want %v", proteinGoal.IdealMax, proteinIdealMax)
	}
	if got := proteinGoal.Status(120); got != goals.StatusIdeal {
		t.Errorf("protein status at 120 = %v, want StatusIdeal", got)
	}

	sugarGoal := goals.Lookup(after, sugarID)
	if sugarGoal.MinValue != nil {
		t.Errorf("sugar min = %v, want nil (never set)", *sugarGoal.MinValue)
	}
	if sugarGoal.IdealMin != nil {
		t.Errorf("sugar ideal min = %v, want nil (never set)", *sugarGoal.IdealMin)
	}
	if sugarGoal.IdealMax == nil || *sugarGoal.IdealMax != sugarIdealMax {
		t.Errorf("sugar ideal max = %v, want %v", sugarGoal.IdealMax, sugarIdealMax)
	}
	if got := sugarGoal.Status(40); got != goals.StatusAcceptable {
		t.Errorf("sugar status at 40 = %v, want StatusAcceptable", got)
	}
}

// TestStore_SaveGoals_ClearsGoal guards against a real bug class seen
// elsewhere in this codebase: re-saving with nil bounds must actually clear
// a previously-set goal, not silently leave the old value in place because
// a NULL update was skipped somewhere.
func TestStore_SaveGoals_ClearsGoal(t *testing.T) {
	store, q := newTestStore(t)
	userID := newTestUser(t, q, "a")

	before, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	calID := nutrientIDByKey(t, before, "calories")

	calMin := 1300.0
	if err := store.SaveGoals(t.Context(), userID, []goals.GoalInput{
		{NutrientID: calID, MinValue: &calMin, MaxValue: nil},
	}); err != nil {
		t.Fatalf("save goals: %v", err)
	}

	if err := store.SaveGoals(t.Context(), userID, []goals.GoalInput{
		{NutrientID: calID, MinValue: nil, MaxValue: nil},
	}); err != nil {
		t.Fatalf("clear goals: %v", err)
	}

	after, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals after clear: %v", err)
	}
	calGoal := goals.Lookup(after, calID)
	if calGoal.MinValue != nil {
		t.Errorf("expected calories min to be cleared, got %v", *calGoal.MinValue)
	}
	if calGoal.Status(1) != goals.StatusNoGoal {
		t.Errorf("expected a cleared goal to report StatusNoGoal, got %v", calGoal.Status(1))
	}
}

// TestStore_ListGoals_IsolatedPerUser guards the same cross-user isolation
// property already enforced for day plans and meal ownership — one user's
// configured ranges must never leak into another user's Configuration page.
func TestStore_ListGoals_IsolatedPerUser(t *testing.T) {
	store, q := newTestStore(t)
	userA := newTestUser(t, q, "a")
	userB := newTestUser(t, q, "b")

	seed, err := store.ListGoals(t.Context(), userA)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	calID := nutrientIDByKey(t, seed, "calories")

	calMin := 1300.0
	if err := store.SaveGoals(t.Context(), userA, []goals.GoalInput{
		{NutrientID: calID, MinValue: &calMin, MaxValue: nil},
	}); err != nil {
		t.Fatalf("save goals for user A: %v", err)
	}

	bGoals, err := store.ListGoals(t.Context(), userB)
	if err != nil {
		t.Fatalf("list goals for user B: %v", err)
	}
	bCalGoal := goals.Lookup(bGoals, calID)
	if bCalGoal.MinValue != nil {
		t.Errorf("expected user B to have no calories goal, got min=%v (leaked from user A)", *bCalGoal.MinValue)
	}
}

// TestStore_SaveGoals_DoesNotTouchInfoURL guards against a real bug found
// during review: SaveGoals and SetInfoURL used to be one combined call that
// unconditionally overwrote the instance-wide info URL with whatever value
// happened to be passed alongside a per-user goal save — so an ordinary
// goals save from one user could silently clobber a more recent info-url
// change made by someone else. They're now separate methods; saving goals
// must never touch the URL.
func TestStore_SaveGoals_DoesNotTouchInfoURL(t *testing.T) {
	store, q := newTestStore(t)
	userID := newTestUser(t, q, "a")

	if err := store.SetInfoURL(t.Context(), "https://example.com/set-by-someone-else"); err != nil {
		t.Fatalf("set info url: %v", err)
	}

	before, err := store.ListGoals(t.Context(), userID)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	calID := nutrientIDByKey(t, before, "calories")
	calMin := 1300.0
	if err := store.SaveGoals(t.Context(), userID, []goals.GoalInput{
		{NutrientID: calID, MinValue: &calMin, MaxValue: nil},
	}); err != nil {
		t.Fatalf("save goals: %v", err)
	}

	url, err := store.InfoURL(t.Context())
	if err != nil {
		t.Fatalf("get info url: %v", err)
	}
	if url != "https://example.com/set-by-someone-else" {
		t.Errorf("info url = %q, want it unchanged by the unrelated goals save", url)
	}
}

func TestStore_InfoURL_SeededDefault(t *testing.T) {
	store, _ := newTestStore(t)

	url, err := store.InfoURL(t.Context())
	if err != nil {
		t.Fatalf("get info url: %v", err)
	}
	if url == "" {
		t.Error("expected migration 00005's seeded default goal_info_url, got empty string")
	}
}

func TestGoal_Status_BoundaryValuesAreInRange(t *testing.T) {
	lo, hi := 100.0, 200.0
	g := goals.Goal{MinValue: &lo, MaxValue: &hi}

	if got := g.Status(100); got != goals.StatusIdeal {
		t.Errorf("Status(100) [== min] = %v, want StatusIdeal", got)
	}
	if got := g.Status(200); got != goals.StatusIdeal {
		t.Errorf("Status(200) [== max] = %v, want StatusIdeal", got)
	}
	if got := g.Status(99.999); got != goals.StatusUnder {
		t.Errorf("Status(99.999) = %v, want StatusUnder", got)
	}
	if got := g.Status(200.001); got != goals.StatusOver {
		t.Errorf("Status(200.001) = %v, want StatusOver", got)
	}
}

// TestGoal_Status_IdealRange covers the full min/ideal/max model — protein's
// shape from the reference table (min=100, ideal=110-130, max=150): under
// the hard floor is still Under (never softened by ideal), inside min/max
// but outside ideal is Acceptable, and inside ideal is Ideal.
func TestGoal_Status_IdealRange(t *testing.T) {
	minV, maxV := 100.0, 150.0
	idealMin, idealMax := 110.0, 130.0
	g := goals.Goal{MinValue: &minV, MaxValue: &maxV, IdealMin: &idealMin, IdealMax: &idealMax}

	cases := []struct {
		total float64
		want  goals.Status
	}{
		{90, goals.StatusUnder},       // below hard min
		{105, goals.StatusAcceptable}, // above min, below ideal
		{110, goals.StatusIdeal},      // == ideal min
		{120, goals.StatusIdeal},      // inside ideal
		{130, goals.StatusIdeal},      // == ideal max
		{140, goals.StatusAcceptable}, // above ideal, below max
		{160, goals.StatusOver},       // above hard max (even though "more protein is better")
	}
	for _, tc := range cases {
		if got := g.Status(tc.total); got != tc.want {
			t.Errorf("Status(%v) = %v, want %v", tc.total, got, tc.want)
		}
	}
}

// TestGoal_Status_IdealMaxOnly covers sugar's shape from the reference
// table: no minimum at all, just an ideal ceiling and a hard ceiling.
func TestGoal_Status_IdealMaxOnly(t *testing.T) {
	maxV, idealMax := 50.0, 30.0
	g := goals.Goal{MaxValue: &maxV, IdealMax: &idealMax}

	if got := g.Status(0); got != goals.StatusIdeal {
		t.Errorf("Status(0) = %v, want StatusIdeal", got)
	}
	if got := g.Status(20); got != goals.StatusIdeal {
		t.Errorf("Status(20) = %v, want StatusIdeal", got)
	}
	if got := g.Status(40); got != goals.StatusAcceptable {
		t.Errorf("Status(40) = %v, want StatusAcceptable (above ideal, below hard max)", got)
	}
	if got := g.Status(60); got != goals.StatusOver {
		t.Errorf("Status(60) = %v, want StatusOver", got)
	}
}

// TestGoal_Status_IdealOnly_NoHardBounds guards that a goal with only ideal
// bounds set (no hard min/max at all) is still a real, non-NoGoal goal.
func TestGoal_Status_IdealOnly_NoHardBounds(t *testing.T) {
	idealMin, idealMax := 10.0, 20.0
	g := goals.Goal{IdealMin: &idealMin, IdealMax: &idealMax}

	if got := g.Status(15); got != goals.StatusIdeal {
		t.Errorf("Status(15) = %v, want StatusIdeal", got)
	}
	if got := g.Status(5); got != goals.StatusAcceptable {
		t.Errorf("Status(5) = %v, want StatusAcceptable (no hard min to violate)", got)
	}
}
