package langkit

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"sync"
)

type Module struct { // TODO maybe other name than domain
	sync.RWMutex
	translations map[string]Translations
}

func NewModule(path string, lazyLoad bool) (*Module, error) {
	d := &Module{translations: make(map[string]Translations)}
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("Could not read translation files at path: %v, err: %w", path, err)
	}

	for _, f := range files {
		ext := filepath.Ext(f.Name())
		if ext == ".po" {
			locale := strings.TrimSuffix(f.Name(), ext)
			fullpath := filepath.Join(path, f.Name())
			translations, err := ReadPoFile(fullpath, lazyLoad)
			if err != nil {
				return nil, fmt.Errorf("Could not read translations at path: %v, err: %w", fullpath, err)
			}
			d.translations[locale] = translations
		}
	}
	return d, nil
}

var noop = &NoTranslations{formatters: newFormatters()}

func (d *Module) FindTranslations(locale string) Translations {
	d.RLock()
	t, found := d.translations[locale]
	d.RUnlock()
	if !found {
		d.Lock()
		defer d.Unlock()

		t = noop
		l := getLang(locale)
		for k, tx := range d.translations {
			if getLang(k) == l {
				t = tx
				break
			}
		}

		d.translations[locale] = t
	}
	return t
}

func getLang(locale string) string {
	parts := strings.Split(locale, "_")
	if len(parts) != 2 {
		parts = strings.Split(locale, "-")
	}
	if len(parts) == 2 {
		return strings.ToLower(parts[0])
	}

	return strings.ToLower(locale)
}
