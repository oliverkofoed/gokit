package form

// Text is a collection of text strings used by the package
type Text struct {
	ErrorRequired       string
	ErrorTooLong        string
	ErrorTooShort       string
	ErrorInvalidEmail   string
	ErrorInvalidWebsite string
	ErrorInvalidDate    string
	ErrorValueBelowMin  string
	ErrorValueAboveMax  string
}

// DefaultText is the default texts used by the package
var DefaultText = Text{
	ErrorRequired:       "This field is required",
	ErrorTooLong:        "This value is too long",
	ErrorTooShort:       "This value is too short",
	ErrorInvalidEmail:   "This is not a valid e-mail address",
	ErrorInvalidWebsite: "This is not a valid website address",
	ErrorInvalidDate:    "This is not a valid date (expected YYYY-MM-DD)",
	ErrorValueBelowMin:  "The minimum accepted value is %v",
	ErrorValueAboveMax:  "The maximum accepted value is %v",
}
