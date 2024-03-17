package langkit

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type Matcher func(filename string, input []byte, printStatus bool) ([]TextMatch, error)

type TextMatch struct {
	Source         string
	Comment        string
	Original       string
	OriginalPlural string
}

type TextExtractor struct {
	base     fs.FS
	includes []string
	excludes []string
	matchers []Matcher
}

func NewTextExtractor(basepath string) *TextExtractor {
	return &TextExtractor{base: os.DirFS(basepath)}
}

func (e *TextExtractor) Include(pathGlob string) {
	e.includes = append(e.includes, pathGlob)
}

func (e *TextExtractor) Exclude(pathGlob string) {
	e.excludes = append(e.excludes, pathGlob)
}

func (e *TextExtractor) Match(matcher Matcher) {
	e.matchers = append(e.matchers, matcher)
}

func (e *TextExtractor) ExtractToP(outputFile string, printStatus bool) {
	if err := e.ExtractTo(outputFile, printStatus); err != nil {
		panic(err)
	}
}

func (e *TextExtractor) ExtractTo(outputFile string, printStatus bool) error {
	files := make(map[string]bool)
	matches := make([]TextMatch, 0)
	for _, glob := range e.includes {
		matches, err := doublestar.Glob(e.base, glob)
		if err != nil {
			return fmt.Errorf("Could not glob '%v', err: %w", glob, err)
		}
		for _, match := range matches {
			files[match] = true
		}
	}
	for _, glob := range e.excludes {
		matches, err := doublestar.Glob(e.base, glob)
		if err != nil {
			return fmt.Errorf("Could not glob '%v', err: %w", glob, err)
		}
		for _, match := range matches {
			delete(files, match)
		}
	}

	flist := make([]string, 0, len(files))
	for file := range files {
		flist = append(flist, file)
	}
	sort.Strings(flist)
	for _, file := range flist {
		buf, err := fs.ReadFile(e.base, file)
		if err != nil {
			return fmt.Errorf("Could not read '%v', err: %w", file, err)
		}

		if printStatus {
			fmt.Println("extracting from:", file)
		}
		for _, matcher := range e.matchers {
			found, err := matcher(file, buf, printStatus)
			if err != nil {
				return fmt.Errorf("Trouble parsing files %v, err: %w", file, err)
			}
			for _, match := range found {
				matches = append(matches, match)
			}
		}
	}

	var sb strings.Builder
	used := make(map[string]bool)
	for i, match := range matches {
		if _, found := used[match.Original]; found {
			continue
		}
		used[match.Original] = true

		if i > 0 {
			sb.WriteString("\n")
		}
		if match.Comment != "" {
			sb.WriteString("#. " + match.Comment + "\n")
		}
		if match.Source != "" {
			sb.WriteString("#: " + match.Source + "\n")
		}
		if match.OriginalPlural == "" {
			sb.WriteString("msgid " + addQuotes(match.Original) + "\n")
			sb.WriteString("msgstr " + addQuotes("") + "\n")
		} else {
			sb.WriteString("msgid " + addQuotes(match.Original) + "\n")
			sb.WriteString("msgid_plural " + addQuotes(match.OriginalPlural) + "\n")
			sb.WriteString("msgstr[0] " + addQuotes("") + "\n")
			sb.WriteString("msgstr[1] " + addQuotes("") + "\n")
		}
	}

	return ioutil.WriteFile(outputFile, []byte(sb.String()), os.ModePerm)
}

func SimpleGetPluralMatcher(prefix string) Matcher {
	return getMatcher(prefix, true)
}
func SimpleGetMatcher(prefix string) Matcher {
	return getMatcher(prefix, false)
}

// GetMatcher returns a matcher that finds all calls to Get starting with the prefix, ending with the postfix.
func getMatcher(prefix string, plural bool) Matcher {
	return func(filename string, input []byte, printStatus bool) ([]TextMatch, error) {
		results := make([]TextMatch, 0)
		scanner := bufio.NewScanner(bytes.NewReader(input))
		lineCounter := 1
		prevLine := ""

		for scanner.Scan() {
			line := scanner.Text()

			for {
				start := strings.Index(line, prefix)
				if start == -1 {
					break
				} else {
					skip := start > 0 && line[start-1:start] == "\""
					line = line[start+len(prefix):]

					if !skip {
						stringArr := extractQuotedStrings(line)
						m := TextMatch{
							Source:  fmt.Sprintf("%v:%v", filename, lineCounter),
							Comment: extractComment(prevLine),
						}
						if len(stringArr) == 0 {
							return nil, fmt.Errorf("parse error/partial match: %v", m.Source)
						} else if len(stringArr) >= 1 {
							m.Original = stringArr[0]
						}
						if plural {
							if len(stringArr) == 1 {
								return nil, fmt.Errorf("parse error/partial match: %v", m.Source)
							} else {
								m.OriginalPlural = stringArr[1]
							}
						}
						if printStatus {
							if plural {
								fmt.Println(" - found: ", m.Original)
								fmt.Println("  plural: ", m.OriginalPlural)
							} else {
								fmt.Println(" - found: ", m.Original)
							}
							fmt.Println()
						}
						results = append(results, m)
					}

				}
			}

			// done with this line
			lineCounter += 1
			prevLine = line
		}

		return results, nil
	}
}

func extractComment(line string) string {
	index := strings.Index(strings.ToLower(line), "translators:")
	if index != -1 {
		return strings.TrimSpace(line[index+len("translators:"):])
	}
	return ""
}

func addQuotes(input string) string {
	return "\"" + input + "\""
}

func extractQuotedStrings(s string) []string {
	results := make([]string, 0)

	inString := false
	quoteChar := ' '
	str := ""
	prev := ' '
	for _, char := range s {
		if inString {
			if char == quoteChar {
				if prev == '\\' {
					str = str + string(char)
				} else {
					results = append(results, str)
					str = ""
					inString = false
				}
			} else {
				str = str + string(char)
			}
		} else if char == '"' || char == '\'' {
			inString = true
			quoteChar = char
		}

		prev = char
	}

	return results
}
