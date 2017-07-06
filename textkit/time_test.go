package textkit

import (
	"testing"
	"time"
)

func TestTime(t *testing.T) {
	entries := []struct {
		minutes  time.Duration
		expected string
	}{
		{1, "just now"},
		{2, "just now"},
		{3, "3 minutes ago"},
		{4, "4 minutes ago"},
		{59, "59 minutes ago"},
		{60, "1 hour ago"},
		{61, "1 hour ago"},
		{62, "1 hour ago"},
		{70, "1.2 hour ago"},
		{80, "1.3 hour ago"},
		{90, "1.5 hour ago"},
		{120, "2 hours ago"},
		{150, "2.5 hours ago"},
		{180, "3 hours ago"},
		{190, "3.2 hours ago"},
		{300, "5 hours ago"},
		{310, "5 hours ago"},
		{360, "6 hours ago"},
		{40 * 60, "40 hours ago"},
		{60 * 24 * 2, "2 days ago"},
		{60 * 24 * 3, "3 days ago"},
		{60 * 24 * 90, "90 days ago"},
	}

	for _, x := range entries {
		now := time.Now()
		stamp := now.Add(-(time.Minute * x.minutes))
		result := TimeAgo(stamp)
		if result != x.expected {
			t.Errorf("Input: %v, was %v, but expected %v", x.minutes*time.Minute, result, x.expected)
			return
		}
	}
}
