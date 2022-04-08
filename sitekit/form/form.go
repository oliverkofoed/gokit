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

	// default texts
	if texts == nil {
		texts = &DefaultText
	}

	// find all fields
	fields := GetFields(form)
	anyUploadFields := false
	for _, field := range fields {
		if _, ok := field.(*FileField); ok {
			anyUploadFields = true
		}
	}
	if anyUploadFields {
		c.Request.ParseMultipartForm(1024 * 500)
	}

	// bind all fieldsk
	for _, field := range fields {
		field.Bind(c, texts)
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

// GetFields reflects over the form and finds the Fields
func GetFields(form interface{}) []Field {
	// ensure we got the right type passed in.
	v := reflect.ValueOf(form)
	if v.Kind() != reflect.Ptr {
		panic("The form argument must be a pointer value.")
	}
	v = reflect.Indirect(v)

	// find all fields
	numFields := v.NumField()
	fields := make([]Field, 0, numFields)
	for i := 0; i < numFields; i++ {
		field := v.Field(i)
		if field.CanInterface() { // note: experimental, added october 18 2021
			if field, ok := (v.Field(i).Addr().Interface()).(Field); ok {
				fields = append(fields, field)
			}
		}
	}

	return fields
}
