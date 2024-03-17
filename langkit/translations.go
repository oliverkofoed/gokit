package langkit

import (
	"fmt"
	"io/ioutil"

	"github.com/leonelquinteros/gotext"
)

type Translations interface {
	Get(original string, formatArgs ...interface{}) string
	GetPlural(original string, originalPlural string, count int, formatArgs ...interface{}) string
}

func ReadPoFile(filepath string, lazyLoad bool) (Translations, error) {
	t := &pofileTranslations{
		filepath:   filepath,
		formatters: newFormatters(),
	}
	if !lazyLoad {
		if err := t.load(); err != nil {
			return nil, err
		}
	}
	return t, nil
}

type pofileTranslations struct {
	filepath   string
	formatters *formatters
	po         *gotext.Po
}

func (t *pofileTranslations) load() error {
	t.po = gotext.NewPo()
	bytes, err := ioutil.ReadFile(t.filepath)
	if err != nil {
		return fmt.Errorf("Could not read .po file at %v. Err: %w", t.filepath, err)
	}
	t.po.Parse(bytes)
	return nil
}

func (t *pofileTranslations) Get(original string, formatArgs ...interface{}) string {
	if t.po == nil {
		if err := t.load(); err != nil {
			panic(err)
		}
	}

	return t.formatters.get(t.po.Get(original)).format(formatArgs...)
}

func (t *pofileTranslations) GetPlural(original string, originalPlural string, count int, formatArgs ...interface{}) string {
	if t.po == nil {
		if err := t.load(); err != nil {
			panic(err)
		}
	}

	return t.formatters.get(t.po.GetN(original, originalPlural, count)).formatPlural(count, formatArgs...)
}

type NoTranslations struct {
	formatters *formatters
}

func (t *NoTranslations) Get(original string, formatArgs ...interface{}) string {
	return t.formatters.get(original).format(formatArgs...)
}

func (t *NoTranslations) GetPlural(original string, originalPlural string, count int, formatArgs ...interface{}) string {
	if count != 1 {
		return t.formatters.get(originalPlural).formatPlural(count, formatArgs...)
	}
	return t.formatters.get(original).format(formatArgs...)
}
