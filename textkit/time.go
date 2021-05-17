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
	fmt.Println(time.Now().Zone())
	fmt.Println(t.Zone())
	fmt.Println(time.Now())
	fmt.Println(t)
	fmt.Println(t.UTC())
	fmt.Println(time.Now().UTC())

	return timeago(t.UTC(), time.Now().UTC())
}

func timeago(t time.Time, now time.Time) string {
	diff := now.Sub(t)
	prefix := ""
	if diff < 0 {
		diff = 0 - diff
		prefix = "-"
	}

	v := ""
	if diff < time.Minute*3 {
		v = "just now"
	} else if diff < time.Minute*60 {
		v = fmt.Sprintf("%v minutes ago", int(diff/time.Minute))
	} else if diff < time.Hour*48 {
		h := int(diff / time.Hour)

		hour := fmt.Sprintf("%.1f", float64(diff)/float64(time.Hour))
		if h >= 5 {
			hour = fmt.Sprintf("%v", int(diff/time.Hour))
		}

		if h == 1 {
			if strings.HasSuffix(hour, ".0") {
				v = fmt.Sprintf("%v hour ago", h)
			} else {
				v = fmt.Sprintf("%v hour ago", hour)
			}
		} else {
			if strings.HasSuffix(hour, ".0") {
				v = fmt.Sprintf("%v hours ago", h)
			} else {
				v = fmt.Sprintf("%v hours ago", hour)
			}
		}
	} else {
		v = fmt.Sprintf("%v days ago", int(diff/(time.Hour*24)))
	}
	return prefix + v

}

// TimeStamp returns an absolute timestamp
func TimeStamp(t time.Time) string {
	return t.Format("Jan 2 15:04 2006 MST")
}
