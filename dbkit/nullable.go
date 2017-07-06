package dbkit

import "time"

// NullableString returns a pointer to the given value, and is useful for inline values to methods
// that require pointer values for nullable columns. E.g., table.Insert(dbkit.NullableString("somevalue"))
func NullableString(value string) *string {
	return &value
}

// NullableBool returns a pointer to the given value, and is useful for inline values to methods
// that require pointer values for nullable columns. E.g., table.Insert(dbkit.NullableBool(false))
func NullableBool(value bool) *bool {
	return &value
}

// NullableInt32 returns a pointer to the given value, and is useful for inline values to methods
// that require pointer values for nullable columns. E.g., table.Insert(dbkit.NullableInt32(345))
func NullableInt32(value int32) *int32 {
	return &value
}

// NullableInt64 returns a pointer to the given value, and is useful for inline values to methods
// that require pointer values for nullable columns. E.g., table.Insert(dbkit.NullableInt64(345))
func NullableInt64(value int64) *int64 {
	return &value
}

// NullableTime returns a pointer to the given value, and is useful for inline values to methods
// that require pointer values for nullable columns. E.g., table.Insert(dbkit.NullableTime(time.Now()))
func NullableTime(value time.Time) *time.Time {
	return &value
}
