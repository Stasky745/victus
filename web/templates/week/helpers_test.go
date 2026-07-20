package week

import (
	"testing"
	"time"
)

func TestFormatWeekLabel(t *testing.T) {
	tests := []struct {
		name  string
		start string
		want  string
	}{
		{
			name:  "within a single month",
			start: "2026-07-06", // Monday .. Sunday 2026-07-12
			want:  "Jul 6–12, 2026",
		},
		{
			name:  "spans a month boundary",
			start: "2026-07-27", // Monday .. Sunday 2026-08-02
			want:  "Jul 27 – Aug 2, 2026",
		},
		{
			name:  "spans a year boundary",
			start: "2025-12-29", // Monday .. Sunday 2026-01-04
			want:  "Dec 29 – Jan 4, 2026",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, err := time.Parse("2006-01-02", tt.start)
			if err != nil {
				t.Fatalf("parse start: %v", err)
			}
			if got := formatWeekLabel(start); got != tt.want {
				t.Errorf("formatWeekLabel(%s) = %q, want %q", tt.start, got, tt.want)
			}
		})
	}
}
