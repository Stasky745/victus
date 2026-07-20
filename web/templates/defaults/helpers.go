// Package defaults renders the "Default Day" page: the meals a user wants
// auto-applied to any day they haven't touched yet. Deliberately reuses
// planning.CategorySection/Item (the same shape the Day Builder renders)
// rather than inventing a parallel type — a default item and a real day
// item are the same thing conceptually, just not tied to a date yet.
package defaults

import (
	"github.com/google/uuid"
)

func idString(id uuid.UUID) string {
	return id.String()
}
