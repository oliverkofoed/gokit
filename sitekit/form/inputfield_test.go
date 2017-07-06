package form

import (
	"html/template"
	"net/http"
	"regexp"
	"testing"

	"github.com/oliverkofoed/gokit/logkit"
	"github.com/oliverkofoed/gokit/sitekit/web"
	"github.com/oliverkofoed/gokit/testkit"
)

func TestInputField(t *testing.T) {
	username := InputField{Name: "username", Caption: "My Text Field", Required: true, Description: "Here you go!"}

	// Assert renders initial value
	testkit.Equal(t, username.HTML(), template.HTML("<input type=\"text\" id=\"username\" name=\"username\" value=\"\">"))

	// assert renderes bound value
	username.Bind(ir("username=oliver"), &DefaultText)
	testkit.Equal(t, username.HTML(), template.HTML("<input type=\"text\" id=\"username\" name=\"username\" value=\"oliver\">"))

	// assert has error when bound with error
	username.Required = true
	username.Bind(ir(""), &DefaultText)
	testkit.Equal(t, username.Error, DefaultText.ErrorRequired)

	// assert implements field
	var iface interface{}
	iface = &username
	if _, ok := iface.(Field); !ok {
		testkit.Fail(t, "InputField does not implement form.Field interface")
	}

	// check that regexp work
	regField := InputField{Name: "username", Regexp: regexp.MustCompile("^[0-9]+$"), RegexpError: "Only numbers please!"}
	regField.Bind(ir("username=01234"), &DefaultText)
	testkit.Equal(t, regField.Error, "")
	regField.Bind(ir("username=abc"), &DefaultText)
	testkit.Equal(t, regField.Error, "Only numbers please!")

	// check that email work
	emailField := InputField{Name: "email", Type: InputTypeEmail}
	emailField.Bind(ir("email=mail@mail.com"), &DefaultText)
	testkit.Equal(t, emailField.Error, "")
	emailField.Bind(ir("email="), &DefaultText)
	testkit.Equal(t, emailField.Error, "") //DefaultText.ErrorInvalidEmail)
	emailField.Required = true
	emailField.Bind(ir("email="), &DefaultText)
	testkit.Equal(t, emailField.Error, DefaultText.ErrorRequired)
	emailField.Bind(ir("email=abc"), &DefaultText)
	testkit.Equal(t, emailField.Error, DefaultText.ErrorInvalidEmail)

	// check that website work
	websiteField := InputField{Name: "url", Type: InputTypeWebsite}
	websiteField.Bind(ir("url=www.google.com"), &DefaultText)
	testkit.Equal(t, websiteField.Error, "")
	testkit.Equal(t, websiteField.Value, "http://www.google.com")
	websiteField.Bind(ir("url=http://www.google.com"), &DefaultText)
	testkit.Equal(t, websiteField.Error, "")
	testkit.Equal(t, websiteField.Value, "http://www.google.com")
	websiteField.Bind(ir("url=http://www.google.com/some/other/path"), &DefaultText)
	testkit.Equal(t, websiteField.Error, "")
	testkit.Equal(t, websiteField.Value, "http://www.google.com/some/other/path")
	websiteField.Bind(ir("url="), &DefaultText)
	testkit.Equal(t, websiteField.Error, "")
	websiteField.Required = true
	websiteField.Bind(ir("url="), &DefaultText)
	testkit.Equal(t, websiteField.Error, DefaultText.ErrorRequired)
	websiteField.Required = false
	websiteField.Bind(ir("url=wwwwwwwwww"), &DefaultText)
	testkit.Equal(t, websiteField.Error, DefaultText.ErrorInvalidWebsite)
}

func ir(input string) *web.Context {
	req, err := http.NewRequest("GET", "/somruri?"+input, nil)
	if err != nil {
		panic(err)
	}
	ctx, done := logkit.Operation(req.Context(), "web.request", logkit.String("method", req.Method), logkit.String("method", req.URL.Path))
	defer done()
	c := web.CreateContext(ctx, nil, nil, nil, req, nil)
	c.PostForm = c.Form // hack for unit tests to use querystring args
	return c
}
