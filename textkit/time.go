package textkit

import (
	"fmt"
	"strings"
	"time"
)

// TimeAgo returns a short time ago string
func TimeAgo(t time.Time) string {
	return timeago(t, time.Now())
}

// TimeAgoUTC returns a short time ago string for an UTC time
func TimeAgoUTC(t time.Time) string {
	return timeago(t, time.Now().UTC())
}

func timeago(t time.Time, now time.Time) string {
	diff := now.Sub(t)
	if diff < time.Minute*3 {
		return "just now"
	} else if diff < time.Minute*60 {
		return fmt.Sprintf("%v minutes ago", int(diff/time.Minute))
	} else if diff < time.Hour*48 {
		h := int(diff / time.Hour)

		hour := fmt.Sprintf("%.1f", float64(diff)/float64(time.Hour))
		if h >= 5 {
			hour = fmt.Sprintf("%v", int(diff/time.Hour))
		}

		if h == 1 {
			if strings.HasSuffix(hour, ".0") {
				return fmt.Sprintf("%v hour ago", h)
			}

			return fmt.Sprintf("%v hour ago", hour)
		}

		if strings.HasSuffix(hour, ".0") {
			return fmt.Sprintf("%v hours ago", h)
		}

		return fmt.Sprintf("%v hours ago", hour)
	}

	return fmt.Sprintf("%v days ago", int(diff/(time.Hour*24)))
}

// TimeStamp returns an absolute timestamp
func TimeStamp(t time.Time) string {
	return t.Format("Jan 2 15:04 2006 MST")
}
