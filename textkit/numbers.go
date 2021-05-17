package textkit

import (
	"fmt"
	"strconv"
	"strings"
)

// ShortNumber makes a number very short
func ShortNumber(number int) string {
	if number < 1000 {
		return strconv.Itoa(number)
	} else if number < 1000000 {
		s := fmt.Sprintf("%1.1fK", float64(number)/float64(1000))
		if strings.HasSuffix(s, ".0K") {
			s = strings.Replace(s, ".0", "", 1)
		}
		return s
	} else if number < 10000000 {
		s := fmt.Sprintf("%1.1fM", float64(number)/float64(1000000))
		if strings.HasSuffix(s, ".0M") {
			s = strings.Replace(s, ".0", "", 1)
		}
		return s
	} else {
		s := fmt.Sprintf("%vM", int(float64(number)/float64(1000000)))
		return s
	}
}
