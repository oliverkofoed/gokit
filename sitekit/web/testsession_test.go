package web

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestTestSession(t *testing.T) {
	session := NewTestSession(t, getTestSite())

	// basic return value test
	session.Get("/robots.txt").AssertBodyEquals("User-agent: *\nDisallow: /\n")
	session.Action(robotsTxt, nil).AssertBodyEquals("User-agent: *\nDisallow: /\n")

	// methods carried correctly
	session.Post("/method", "", nil).AssertBodyEquals("POST\n")
	session.Get("/method").AssertBodyEquals("GET\n")

	// test not-found
	session.Get("/fluffa").AssertBodyEquals("Not Found:/fluffa")

	// ensure cookies are carried
	session.Get("/cookiecounter").AssertBodyEquals("0\n")
	session.Get("/cookiecounter").AssertBodyEquals("1\n")
	session.Get("/cookiecounter").AssertBodyEquals("2\n")
	session.Get("/cookiecounter").AssertBodyEquals("3\n")
}

func getTestSite() *Site {
	site := NewSite(true, "/a/")
	//site.DefaultMasterFile = "/views/master.tmpl"
	//site.Assets.AddDirectory(".", "/")
	//site.Assets.AddDirectory("css", "/css/")
	//site.Assets.AddDirectory("views", "/views/")
	//site.Assets.AddDirectory("images", "/images/")
	//site.Assets.AddDirectory("js", "/js/")
	//site.Assets.AddPreprocessor(extension, processor) //TODO: maybe use to add in_autogo
	//site.AddRoute(Route{Path: "/", Action: index, Template: "/views/home.tmpl"})
	//site.AddRoute(Route{Path: "/account/signin", Action: account.SignIn, Template: "/account/signin.tmpl"})
	//site.AddRoute(Route{Path: "/account/signup", Action: account.SignUp, Template: "/account/signup.tmpl"})
	site.AddRoute(Route{Path: "/robots.txt", Action: robotsTxt})
	site.AddRoute(Route{Path: "/method", Action: method})
	site.AddRoute(Route{Path: "/cookiecounter", Action: cookieCounter})
	site.NotFound = Route{Action: notFound}

	return site
}

func notFound(c *Context) {
	c.Write([]byte("Not Found:" + c.Request.URL.Path))
}

func robotsTxt(c *Context) {
	fmt.Fprintln(c, "User-agent: *")
	fmt.Fprintln(c, "Disallow: /")
}

func method(c *Context) {
	fmt.Fprintln(c, c.Request.Method)
}

func cookieCounter(c *Context) {
	counter, err := c.Cookie("counter")

	value := int64(0)
	if counter != nil && err == nil {
		value, err = strconv.ParseInt(counter.Value, 10, 32)
		if err != nil {
			value = 0
		}
	}

	http.SetCookie(c, &http.Cookie{
		Name:  "counter",
		Value: fmt.Sprintf("%v", value+1),
	})

	fmt.Fprintln(c, fmt.Sprintf("%v", value))
}
