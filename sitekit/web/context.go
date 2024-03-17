package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"

	"github.com/julienschmidt/httprouter"
	"github.com/oliverkofoed/gokit/logkit"
)

type Context struct {
	*logkit.Context
	Site       *Site
	Route      *Route
	params     httprouter.Params
	data       map[string]interface{}
	w          http.ResponseWriter
	Request    *http.Request
	MasterFile string

	Form     formInputReader
	PostForm formInputReader
	Cookies  cookieInputReader
}

func CreateContext(ctx *logkit.Context, site *Site, route *Route, w http.ResponseWriter, req *http.Request, params httprouter.Params) *Context {
	return &Context{
		Context:  ctx,
		Site:     site,
		Route:    route,
		params:   params,
		w:        w,
		Request:  req,
		Form:     formInputReader{request: req, usePostForm: false},
		PostForm: formInputReader{request: req, usePostForm: true},
		Cookies:  cookieInputReader{request: req},
	}
}

func (c *Context) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return c.w.(http.Hijacker).Hijack()
}

func (c *Context) RemoveData(key string) {
	if c.data != nil {
		delete(c.data, key)
	}
}

func (c *Context) SetData(key string, value interface{}) {
	if c.data == nil {
		c.data = make(map[string]interface{})
	}

	c.data[key] = value
}

func (c *Context) GetData(key string) (interface{}, bool) {
	v, ok := c.data[key]
	return v, ok
}

func (c *Context) RouteArg(name string) string {
	return c.params.ByName(name)
}

func (c *Context) RouteArgInt64(name string) (int64, error) {
	return strconv.ParseInt(c.params.ByName(name), 10, 64)
}

func (c *Context) Header() http.Header {
	return c.w.Header()
}

func (c *Context) Write(data []byte) (int, error) {
	return c.w.Write(data)
}

func (c *Context) Flush() {
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *Context) WriteString(value string) (int, error) {
	return io.WriteString(c.w, value)
}

func (c *Context) WriteHeader(statusCode int) {
	c.w.WriteHeader(statusCode)
}

func (c *Context) Render(data interface{}) {
	c.RenderTemplate(c.Route.Template, data)
}

func (c *Context) Cookie(name string) (*http.Cookie, error) {
	return c.Request.Cookie(name)
}

func (c *Context) JSON(data interface{}) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	_, err = c.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}

func (c *Context) RenderTemplate(templatePath string, data interface{}) error {
	master := c.MasterFile
	if master == "" {
		master = c.Route.MasterTemplate
	}
	if master == "" {
		master = c.Site.DefaultMasterFile
	}
	if master == "none" {
		master = ""
	}

	if c.Site.TemplateDataWrapper != nil {
		var err error
		data, err = c.Site.TemplateDataWrapper(c, data)
		if err != nil {
			fmt.Println("Site.TemplateDataWrapper error: ", err)
			return err
		}
	}

	templateFiles := []string{templatePath, master}
	if master == "" {
		templateFiles = templateFiles[0:1]
	}

	err := c.Site.Assets.RenderTemplate(templateFiles, c.w, data)
	if err != nil {
		fmt.Println("RenderTemplate error: ", err, templatePath, master)
		return err
	}

	return nil
}

func (c *Context) RedirectPermanent(urlStr string) {
	http.Redirect(c, c.Request, urlStr, 301)
}

func (c *Context) Redirect(urlStr string) {
	http.Redirect(c, c.Request, urlStr, 302)
}

func (c *Context) NotFound() {
	if c.Site.NotFound.Action != nil {
		c.w.WriteHeader(404)
		c.Site.runRoute(&c.Site.NotFound, c.w, c.Request, make(httprouter.Params, 0, 0), true)
	} else {
		http.NotFound(c, c.Request)
	}
}

func (c *Context) CheckErr(err error) {
	if err != nil {
		panic(err.Error())
		//c.Panic(err.Error())
	}
}

func (c *Context) ServerError(err string, code int) {
	if c.Site.Development {
		fmt.Println("ServerError:", err)
		debug.PrintStack()
	}
	if c.Site.ServerError.Action != nil {
		c.w.WriteHeader(code)
		c.Site.runRoute(&c.Site.ServerError, c.w, c.Request, make(httprouter.Params, 0, 0), true)
	} else {
		http.Error(c, err, code)
	}
}

// ClientIP trys to get the ip of the client by inspecting
// common headers and the ip of remote endpoint of the tcp connection
func (c *Context) ClientIP() net.IP {
	ip := ""
	if header := c.Request.Header.Get("CF-Connecting-IP"); header != "" {
		ip = header
	} else if header := c.Request.Header.Get("X-Forwarded-For"); header != "" {
		ip = header
	} else {
		remoteIP, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			return []byte{}
		}
		ip = remoteIP
	}

	userIP := net.ParseIP(ip)
	if userIP == nil {
		return net.IP{}
	}
	return userIP
}
