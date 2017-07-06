package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// InputReader makes it easy to read input
type InputReader interface {
	String(name string, fallback string) string
	Bool(name string, fallback bool) bool
	Int(name string, min, max, fallback int) int
}

type formInputReader struct {
	request     *http.Request
	values      *url.Values
	usePostForm bool
}

func (f formInputReader) get(name string) string {
	if f.values == nil {
		f.request.ParseForm()
		if f.usePostForm {
			f.values = &f.request.PostForm
		} else {
			f.values = &f.request.Form
		}
	}

	return f.values.Get(name)
}

func (f formInputReader) String(name string, fallback string) string {
	if v := f.get(name); v != "" {
		return v
	}

	return fallback
}

func (f formInputReader) Bool(name string, fallback bool) bool {
	if v := f.get(name); v != "" {
		l := strings.ToLower(v)
		return l == "true" || l == "1" || l == "on" || l == "yes"
	}

	return fallback
}

func (f formInputReader) Int(name string, min, max, fallback int) int {
	if v := f.get(name); v != "" {
		i, err := strconv.ParseInt(v, 10, 0)
		if err != nil {
			return fallback
		}

		iInt := int(i)
		if iInt >= min && iInt <= max {
			return iInt
		}
	}

	return fallback
}

type cookieInputReader struct {
	request *http.Request
}

func (f cookieInputReader) get(name string) string {
	if c, err := f.request.Cookie(name); err == nil && c != nil {
		return c.Value
	}

	return ""
}

func (f cookieInputReader) String(name string, fallback string) string {
	if v := f.get(name); v != "" {
		return v
	}

	return fallback
}

func (f cookieInputReader) Bool(name string, fallback bool) bool {
	if v := f.get(name); v != "" {
		l := strings.ToLower(v)
		return l == "true" || l == "1" || l == "on" || l == "yes"
	}

	return fallback
}

func (f cookieInputReader) Int(name string, min, max, fallback int) int {
	if v := f.get(name); v != "" {
		i, err := strconv.ParseInt(v, 10, 0)
		if err == nil {

			return fallback
		}

		iInt := int(i)
		if iInt >= min && iInt <= max {
			return iInt
		}
	}

	return fallback
}
