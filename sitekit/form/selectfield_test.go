package form

import (
	"html/template"
	"testing"

	"github.com/oliverkofoed/gokit/testkit"
)

func TestSelectField(t *testing.T) {
	age := SelectField{Name: "selectfield", Caption: "My Text Field", Required: true, Description: "Here you go!", Options: []*Option{
		&Option{Caption: "10-20", Name: "age10-20", Value: "[10:20]"},
		&Option{Caption: "20-30", Name: "age20-30", Value: "[20:30]"},
		&Option{Caption: "Above 30", Value: "30+"},
	}}

	// Assert renders initial value
	testkit.Equal(t, age.HTML(), template.HTML("<select id=\"selectfield\" name=\"selectfield\"><option value=\"age10-20\">10-20</option><option value=\"age20-30\">20-30</option><option value=\"Above30_2\">Above 30</option></select>"))

	// assert renderes bound value

	age.Bind(ir("selectfield=age20-30"), &DefaultText)
	testkit.Equal(t, age.HTML(), template.HTML("<select id=\"selectfield\" name=\"selectfield\"><option value=\"age10-20\">10-20</option><option value=\"age20-30\" selected=\"selected\">20-30</option><option value=\"Above30_2\">Above 30</option></select>"))
	testkit.Equal(t, age.Value, "[20:30]")

	// assert has error when bound with error
	age.Required = true
	age.Bind(ir(""), &DefaultText)
	testkit.Equal(t, age.Error, DefaultText.ErrorRequired)

	// assert implements field
	var iface interface{}
	iface = &age
	if _, ok := iface.(Field); !ok {
		testkit.Fail(t, "InputField does not implement form.Field interface")
	}
}
