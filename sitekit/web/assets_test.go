package web

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestFileSystem(t *testing.T) {
	f := NewAssets("/a/")

	testkit.NoError(t, f.AddDirectory("testassets", "/"))

	// look for unknown file
	file, err := f.Get("/css/unknown.css")
	testkit.Assert(t, file == nil)
	testkit.Assert(t, err != nil)

	// get url for unknown file
	url, err := f.GetUrl("/css/unknown.css")
	testkit.Error(t, err)

	// get unknown file
	w := httptest.NewRecorder()
	f.Serve("/", w, nil)
	testkit.Assert(t, w.Code == 404)
	f.Serve("/wrong/", w, nil)
	testkit.Assert(t, w.Code == 404)
	f.Serve("/a/bad", w, nil)
	testkit.Assert(t, w.Code == 404)

	// find a file
	file, err = f.Get("/css/test.css")
	testkit.NoError(t, err)
	testkit.Assert(t, file != nil)
	testkit.Equal(t, file.path, "testassets/css/test.css")
	testkit.Equal(t, file.HashString, "18b07bc34c47cb08bf8454d478188d8cac0c624f")
	testkit.Equal(t, file.ContentType, "text/css; charset=utf-8")

	// get url for file
	url, err = f.GetUrl("/css/test.css")
	testkit.Assert(t, url == "/a/18b07bc34c47cb08bf8454d478188d8cac0c624f")
	testkit.Assert(t, err == nil)

	// serve file
	w = httptest.NewRecorder()
	f.Serve(url, w, nil)
	testkit.Assert(t, w.Code == 200)
	testkit.Assert(t, reflect.DeepEqual(w.Body.Bytes(), file.Content))
	testkit.Assert(t, w.Header().Get("Content-Type") == file.ContentType)

	// templates
	w = httptest.NewRecorder()
	testkit.NoError(t, f.RenderTemplate([]string{"/templates/index.tmpl", "/templates/master.tmpl"}, w, nil))
	testkit.Equal(t, string(w.Body.Bytes()), "MASTER[body-content]\nSIDEBAR[default-sidebar]")

	w = httptest.NewRecorder()
	testkit.NoError(t, f.RenderTemplate([]string{"/templates/index_sidebar.tmpl", "/templates/master.tmpl"}, w, nil))
	testkit.Equal(t, string(w.Body.Bytes()), "MASTER[body-content]\nSIDEBAR[sidebar-content]")

	w = httptest.NewRecorder()
	testkit.NoError(t, f.RenderTemplate([]string{"/templates/funcs.tmpl"}, w, nil))
	testkit.Equal(t, string(w.Body.Bytes()), string(file.Content)+"\n/a/"+file.HashString)
}
