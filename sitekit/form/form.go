package form

import (
	"reflect"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// Complete checks if the current request is a POST, and if so binds all form.Fields from the given form
func Complete(c *web.Context, form interface{}, texts *Text) bool {
	// only work if POST.
	if c.Request.Method != "POST" {
		return false
	}

	// ensure we got the right type passed in.
	v := reflect.ValueOf(form)
	if v.Kind() != reflect.Ptr {
		panic("The form argument must be a pointer value.")
	}
	v = reflect.Indirect(v)

	// default texts
	if texts == nil {
		texts = &DefaultText
	}

	// bind all fields
	numFields := v.NumField()
	fields := make([]Field, 0, numFields)
	for i := 0; i < numFields; i++ {
		if field, ok := (v.Field(i).Addr().Interface()).(Field); ok {
			field.Bind(c, texts)
			fields = append(fields, field)
		}
	}

	// check all fields are valid
	for _, v := range fields {
		_, _, _, err := v.GetRenderDetails()
		if err != "" {
			return false
		}
	}
	return true
}
