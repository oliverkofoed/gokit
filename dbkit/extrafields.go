package dbkit

import (
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
)

func (s *Schema) ReadExtraFieldsFile(filename string, log func(msg string, args ...interface{})) error {
	// read the entire file
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	// parse file
	anyError := false
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		tableName, property, gotype, importname, err := parseExtraFieldLine(line)
		if err != nil {
			log("Error: invalid extrafields file '%v': %v", filename, err.Error())
			anyError = true
			continue
		}

		var table *Table
		for _, t := range s.Tables {
			if t.StructName == tableName {
				table = t
				break
			}
		}
		if table == nil {
			log("Unknown table '%v' for extra fields from '%v' line '%v'", tableName, filename, line)
			continue
		}

		table.ExtraFields = append(table.ExtraFields, &ExtraField{
			Name:       property,
			GoTypeName: gotype,
			Import:     importname,
		})
	}

	if anyError {
		return fmt.Errorf("Errors parsing extrafields file: %v", filename)
	}
	return nil
}

var extraFieldParseErrorTmpl = "Error parsing extrafields from line: '%v'. Expected format is 'table.propertype gotype optional-import', like 'User.IsAwesome bool' or 'User.projects *projectsStore' or 'Stats.Counters *counters.Counter github.com/someuser/counters'"
var extraFieldParser = regexp.MustCompile("^\\s?(\\w+)\\.(\\w+)\\s*(\\*?\\s*[\\w|\\.]+)(\\s+([\\w|\\.|\\/]+))?")

func parseExtraFieldLine(line string) (table string, property string, gotype string, importname string, err error) {
	matches := extraFieldParser.FindStringSubmatch(line)
	if len(matches) != 6 {
		return "", "", "", "", fmt.Errorf(extraFieldParseErrorTmpl, line)
	}

	return matches[1], matches[2], matches[3], matches[5], nil
}
