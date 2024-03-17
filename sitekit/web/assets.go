package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/css"
	"github.com/tdewolff/minify/html"
	"github.com/tdewolff/minify/js"
	"github.com/tdewolff/minify/svg"
)

// SingleServerSite can be set to true if you know you're never going to
// deploy to a cluster of multiple web servers (like during development).
// This will make startup faster as assets can be procssed ad-hoc instead of
// all up front.
var SingleServerSite = false

type Preprocessor func(assets *Assets, path string, content []byte) (result []byte, err error)

type Assets struct {
	version              int
	baseURL              string
	lock                 sync.RWMutex
	preprocessors        map[string][]Preprocessor
	entries              map[string]*File
	byChecksum           map[string]*File
	templateCache        map[string]*template.Template
	templateCacheVersion int
	templateFuncMap      template.FuncMap
}

type File struct {
	path           string
	Content        []byte
	ContentGZipped []byte
	Hash           []byte
	HashString     string
	ContentType    string
}

func NewAssets(baseURL string) Assets {
	assets := Assets{
		version:              0,
		baseURL:              baseURL,
		preprocessors:        make(map[string][]Preprocessor),
		entries:              make(map[string]*File),
		byChecksum:           make(map[string]*File),
		templateCache:        make(map[string]*template.Template),
		templateCacheVersion: 0,
	}
	assets.templateFuncMap = template.FuncMap{
		"jscode": func(input string) template.JS { return template.JS(input) },
		"asset": func(virtualPath string) (string, error) {
			if virtualPath[0] != '/' {
				return "", errors.New("path argument must start with '/'")
			}
			return assets.GetUrl(virtualPath)
		},
		"assetinline": func(virtualPath string) (string, error) {
			if virtualPath[0] != '/' {
				return "", errors.New("path argument must start with '/'")
			}
			file, err := assets.Get(virtualPath)
			if err != nil {
				return "", err
			}
			return string(file.Content), nil
		},
	}

	assets.AddPreprocessor(".css", AssetCssPreprocessor)
	assets.AddPreprocessor(".css", AssetSourceMapPreprocessor)

	return assets
}

func (f *Assets) AddMinifyPreprocessors(minifyCSS, minifyJavascript, minifySVG, minifyHTML, minifyTmpl bool) {
	m := minify.New()
	minifier := func(mimeType string) func(assets *Assets, path string, content []byte) ([]byte, error) {
		return func(assets *Assets, path string, content []byte) ([]byte, error) {
			minified, err := m.Bytes(mimeType, content)
			if err != nil {
				return nil, err
			}
			return minified, nil
		}
	}
	if minifyJavascript {
		m.AddFunc("text/javascript", js.Minify)
		f.AddPreprocessor(".js", minifier("text/javascript"))
	}
	if minifySVG {
		m.AddFunc("image/svg+xml", svg.Minify)
		f.AddPreprocessor(".svg", minifier("image/svg+xml"))
	}
	if minifyCSS {
		m.Add("text/css", &css.Minifier{
			Decimals: -1,
		})
		f.AddPreprocessor(".css", minifier("text/css"))
	}
	if minifyHTML {
		m.Add("text/html", &html.Minifier{
			KeepDefaultAttrVals: true,
			KeepDocumentTags:    true,
			KeepEndTags:         true,
		})
		f.AddPreprocessor(".htm", minifier("text/html"))
		f.AddPreprocessor(".html", minifier("text/html"))
	}
	if minifyTmpl {
		// special case for golang templates
		randomIDRunes := []rune("abcdefghijklmnopqrstuvwxyz")
		golangTagRegexp := regexp.MustCompile("{{[^}]+}}")
		placeholdertag := regexp.MustCompile("placeholder[a-z]+?placeholder")

		f.AddPreprocessor(".tmpl", func(assets *Assets, path string, content []byte) ([]byte, error) {
			store := make(map[string][]byte)

			// replace golang template tags with placeholders
			content = golangTagRegexp.ReplaceAllFunc(content, func(input []byte) []byte {
				b := make([]rune, 20)
				for i := range b {
					b[i] = randomIDRunes[rand.Intn(len(randomIDRunes))]
				}
				id := "placeholder" + string(b) + "placeholder"
				store[id] = input
				return []byte(id)
			})

			minified, err := m.Bytes("text/html", content)
			if err != nil {
				return nil, err
			}

			// replaceplaceholders with golang tags
			minified = placeholdertag.ReplaceAllFunc(minified, func(input []byte) []byte {
				if tag, found := store[string(input)]; found {
					return tag
				}
				return input
			})

			return minified, nil
		})
	}
}

func (f *Assets) SetTemplateFunc(name string, templateFunc interface{}) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.templateFuncMap[name] = templateFunc
}

func (f *Assets) AddDirectory(directory string, virtualPath string, ignoreDirs ...string) error {
	return filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			if ignoreDirs != nil {
				for _, name := range ignoreDirs {
					if info.Name() == name {
						return filepath.SkipDir
					}
				}
			}
		} else {
			rel, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}

			f.AddFile(path, virtualPath+getURLPathOfFile(rel))
		}

		return err
	})
}

func (f *Assets) AddPreprocessor(extension string, processor Preprocessor) {
	f.lock.Lock()
	defer f.lock.Unlock()

	preprocessors := f.preprocessors[extension]
	if preprocessors == nil {
		preprocessors = make([]Preprocessor, 0, 10)
	}

	f.preprocessors[extension] = append(preprocessors, processor)
}

func (f *Assets) ClearPreprocessors(extension string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	delete(f.preprocessors, extension)
}

func (f *Assets) AddFile(file string, virtualPath string) {
	ff := &File{path: file}

	f.lock.Lock()
	f.entries[virtualPath] = ff
	f.version++
	f.lock.Unlock()

	if !SingleServerSite {
		f.setContent(ff, virtualPath)
	}
}

func (f *Assets) Get(virtualPath string) (*File, error) {
	f.lock.RLock()
	file := f.entries[virtualPath]
	f.lock.RUnlock()
	if file == nil {
		return nil, errors.New("File Not Found: " + virtualPath)
	}

	if file.Content == nil {
		err := f.setContent(file, virtualPath)
		if err != nil {
			return nil, err
		}
	}

	return file, nil
}

func (f *Assets) setContent(file *File, virtualPath string) error {
	// read file content
	fileContent, err := ioutil.ReadFile(file.path)
	if err != nil {
		return err
	}

	// figure out content type
	extension := filepath.Ext(file.path)
	file.ContentType = mime.TypeByExtension(extension)
	if file.ContentType == "" {
		file.ContentType = http.DetectContentType(fileContent)
	}

	// preprocess content
	f.lock.RLock()
	preprocessors := f.preprocessors[extension]
	f.lock.RUnlock()
	if preprocessors != nil {
		for _, processor := range preprocessors {
			newContent, err := processor(f, virtualPath, fileContent)
			if err != nil {
				return err
			}

			fileContent = newContent
		}
	}

	// gzip content
	var buffer bytes.Buffer
	compressor := gzip.NewWriter(&buffer)
	compressor.Write(fileContent)
	compressor.Close()
	file.ContentGZipped = buffer.Bytes()

	// sha1 the content.
	h := sha1.New()
	h.Write(fileContent)
	file.Hash = h.Sum(nil)
	file.HashString = hex.EncodeToString(file.Hash)
	f.lock.Lock()
	f.byChecksum[file.HashString] = file
	f.lock.Unlock()

	// set the content (this is done last to minimize the chance of two goroutines in this if-statement)
	file.Content = fileContent
	return nil
}

func (f *Assets) GetUrl(virtualPath string) (string, error) { //todo: returns /a/<checksum> w/ forever expires.
	file, err := f.Get(virtualPath)
	if err != nil {
		return "", err
	}

	return f.baseURL + file.HashString, nil
}

func (f *Assets) Serve(url string, w http.ResponseWriter, r *http.Request) {
	if len(url) < len(f.baseURL) {
		httpError(w, 404, "404 - File not found")
		return
	}

	checksum := url[len(f.baseURL):]
	f.lock.RLock()
	file := f.byChecksum[checksum]
	f.lock.RUnlock()

	if file == nil {
		httpError(w, 404, "404 - File not found")
		return
	}

	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31556926")
	w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).Format(http.TimeFormat))

	if r != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(file.ContentGZipped)
	} else {
		w.Write(file.Content)
	}
}

func (f *Assets) RenderTemplateString(templatePathArr []string, data interface{}) (string, error) {
	return f.RenderNamedTemplateString(templatePathArr, templatePathArr[len(templatePathArr)-1], data)
}

func (f *Assets) RenderNamedTemplateString(templatePathArr []string, name string, data interface{}) (string, error) {
	t, err := f.GetTemplate(templatePathArr)

	if err != nil {
		return "", err
	}

	buf := bytes.NewBuffer(nil)
	err = t.ExecuteTemplate(buf, name, data)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (f *Assets) RenderTemplate(templatePathArr []string, w http.ResponseWriter, data interface{}) error {
	return f.RenderNamedTemplate(templatePathArr, templatePathArr[len(templatePathArr)-1], w, data)
}

func (f *Assets) RenderNamedTemplate(templatePathArr []string, name string, w http.ResponseWriter, data interface{}) error {
	t, err := f.GetTemplate(templatePathArr)

	if err != nil {
		httpError(w, 500, err.Error())
		return err
	}

	err = t.ExecuteTemplate(w, name, data)
	if err != nil {
		httpError(w, 500, err.Error())
		return err
	}
	return nil
}

func (f *Assets) GetTemplate(templatePathArr []string) (*template.Template, error) {
	// reset cache if filesystem has changed
	if f.version != f.templateCacheVersion {
		f.lock.Lock()
		f.templateCacheVersion = f.version
		f.templateCache = make(map[string]*template.Template)
		f.lock.Unlock()
	}

	// check cache
	cacheKey := strings.Join(templatePathArr, "<")
	f.lock.RLock()
	tmpl := f.templateCache[cacheKey]
	f.lock.RUnlock()
	if tmpl != nil {
		return tmpl, nil
	}

	// not found in cache, create new.
	tmpl = template.New("temp-outer-template-shell").Funcs(f.templateFuncMap)

	for _, path := range templatePathArr {
		if path != "" {
			file, err := f.Get(path)
			if err != nil {
				return nil, err
			}

			temp, err := template.New(path).Funcs(f.templateFuncMap).Parse(string(file.Content))
			if err != nil {
				return nil, errors.New(path + ": " + err.Error())
			}

			for _, t := range temp.Templates() {
				if tmpl.Lookup(t.Name()) == nil {
					//fmt.Println("==================> "+t.Name()+" = "+path, string(file.content), t.Tree)
					tmpl.AddParseTree(t.Name(), t.Tree)
				}
			}
		}
	}

	f.lock.Lock()
	f.templateCache[cacheKey] = tmpl
	f.lock.Unlock()

	return tmpl, nil
}

func httpError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintln(w, message)
}

func getURLPathOfFile(path string) string {
	return strings.Replace(path, "\\", "/", -1)
}

func (f *Assets) getRooted(fromFile string, targetFile string) (string, error) {
	if targetFile[0] == '/' {
		return targetFile, nil
	}
	return getURLPathOfFile(filepath.Join(filepath.Dir(fromFile), targetFile)), nil
}

// ------
var cssUrlRegex = regexp.MustCompile(`url\([^\)]+\)`)
var sourceMapRegex = regexp.MustCompile(`sourceMappingURL=\S+`)

func AssetCssPreprocessor(assets *Assets, path string, content []byte) ([]byte, error) {
	return replaceProcessor(assets, path, content, cssUrlRegex, "url(", ")")
}

func AssetSourceMapPreprocessor(assets *Assets, path string, content []byte) ([]byte, error) {
	return replaceProcessor(assets, path, content, sourceMapRegex, "sourceMappingURL=", "")
}

func replaceProcessor(assets *Assets, path string, content []byte, regex *regexp.Regexp, prefix string, postfix string) ([]byte, error) {
	var replaceErr error = nil
	newContent := regex.ReplaceAllFunc(content, func(match []byte) []byte {
		//fmt.Println("Match: " + string(match))
		file := string(match)[len(prefix) : len(match)-len(postfix)]

		quoted := strings.HasPrefix(file, "'") && strings.HasSuffix(file, "'")
		quoted = quoted || (strings.HasPrefix(file, "\"") && strings.HasSuffix(file, "\""))
		if quoted {
			file = file[1 : len(file)-1]
		}

		if strings.HasPrefix(file, "data:") || strings.HasPrefix(file, "\"data:") || strings.HasPrefix(file, "base64:") || strings.HasPrefix(file, "\"base64:") {
			return []byte(file)
		}

		inlineBase64 := false
		if strings.HasPrefix(file, "inlinebase64:") {
			file = file[7:]
			inlineBase64 = true
		}

		// root the path
		rootedPath, err := assets.getRooted(path, strings.TrimSpace(file))
		if err != nil {
			replaceErr = err
			return match
		}

		// inline base64 support
		if inlineBase64 {
			f, err := assets.Get(rootedPath)
			if err != nil {
				replaceErr = err
				return match
			}
			var buf bytes.Buffer
			buf.WriteString("data:")
			buf.WriteString(f.ContentType)
			buf.WriteString(";base64,")
			buf.WriteString(base64.StdEncoding.EncodeToString(f.Content))
			return buf.Bytes()
		}

		// get the url from asset system
		url, err := assets.GetUrl(rootedPath)
		if err != nil {
			replaceErr = err
			return match
		}
		return []byte(prefix + url + postfix)
	})

	if replaceErr != nil {
		return nil, replaceErr
	}

	return newContent, nil
}
