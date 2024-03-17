package langkit

import (
	"context"
	"errors"
)

var ForContext func(ctx context.Context) Translations

func forContext(ctx context.Context) Translations {
	if ForContext == nil {
		panic(errors.New("Please set langkit.ForContext before attempting operations based on context lookup"))
	}
	return ForContext(ctx)
}

func Get(ctx context.Context, original string, formatArgs ...interface{}) string {
	return forContext(ctx).Get(original, formatArgs...)
}

func GetPlural(ctx context.Context, original string, originalPlural string, count int, formatArgs ...interface{}) string {
	return forContext(ctx).GetPlural(original, originalPlural, count, formatArgs...)
}
