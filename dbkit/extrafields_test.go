package dbkit

import (
	"fmt"
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestExtraFieldParser(t *testing.T) {
	tests := []struct {
		line       string
		table      string
		property   string
		gotype     string
		importname string
		err        bool
	}{
		{"badinput", "", "", "", "", true},
		{"User.isActive bool", "User", "isActive", "bool", "", false},
		{"User.Friends *userFriends", "User", "Friends", "*userFriends", "", false},
		{"Stats.Counters counters.Counter github.com/someuser/counters", "Stats", "Counters", "counters.Counter", "github.com/someuser/counters", false},
		{"Stats.Counters *counters.Counter github.com/someuser/counters", "Stats", "Counters", "*counters.Counter", "github.com/someuser/counters", false},
	}
	for _, d := range tests {
		table, property, gotype, importname, err := parseExtraFieldLine(d.line)
		if d.err {
			testkit.Equal(t, fmt.Sprintf(extraFieldParseErrorTmpl, d.line), err.Error())
		} else {
			testkit.Equal(t, table, d.table)
			testkit.Equal(t, property, d.property)
			testkit.Equal(t, gotype, d.gotype)
			testkit.Equal(t, importname, d.importname)
		}
	}
}
