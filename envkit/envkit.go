package envkit

import (
	"fmt"
	"os"
)

func String(name string, defaultValue string) string {
	v := os.Getenv(name)
	if v == "" {
		v = defaultValue
	}
	return v
}

func StringRequired(name string, usefallback bool, fallback string) string {
	v := os.Getenv(name)
	if v != "" {
		return v
	}

	if usefallback {
		return fallback
	}

	panic(fmt.Sprintf("unspecified environment variable: %v", name))
}
