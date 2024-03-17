package langkit_test

import (
	"context"
	"testing"

	"github.com/oliverkofoed/gokit/langkit"
	"github.com/oliverkofoed/gokit/testkit"
)

// Get text from context

func TestLang(t *testing.T) {
	module, err := langkit.NewModule("testmodule", false)
	testkit.NoError(t, err)

	danish := module.FindTranslations("da_DK")

	testkit.Equal(t, danish.Get("Hello World"), "Hej til verden")
	testkit.Equal(t, danish.GetPlural("You have one message", "You have {plural} messages", 1), "Du har en besked")
	testkit.Equal(t, danish.GetPlural("You have one message", "You have {plural} messages", 2), "Du har 2 beskeder")

	testkit.Equal(t, danish.GetPlural("You have one message from {1}", "You have {plural} messages from {1}", 1, "Oliver"), "Oliver har sendt dig en besked")
	testkit.Equal(t, danish.GetPlural("You have one message from {1}", "You have {plural} messages from {1}", 2, "Oliver"), "Oliver har sendt dig 2 beskeder")

	testkit.Equal(t, danish.Get("We don't have this text"), "We don't have this text") // something that's not translated

	original := module.FindTranslations("en_US")
	testkit.Equal(t, original.Get("Hello World"), "Hello World")
	testkit.Equal(t, original.GetPlural("You have one message", "You have {plural} messages", 1), "You have one message")
	testkit.Equal(t, original.GetPlural("You have one message", "You have {plural} messages", 2), "You have 2 messages")
	testkit.Equal(t, original.GetPlural("You have one message from {1}", "You have {plural} messages from {1}", 1, "Oliver"), "You have one message from Oliver")
	testkit.Equal(t, original.GetPlural("You have one message from {1}", "You have {plural} messages from {1}", 2, "Oliver"), "You have 2 messages from Oliver")
	testkit.Equal(t, original.Get("We don't have this text"), "We don't have this text") // not translated
}

func TestContextLang(t *testing.T) {
	bg := context.Background()

	langkit.ForContext = func(ctx context.Context) langkit.Translations {
		if ctx != bg {
			panic("expected correct context")
		}

		domain, err := langkit.NewModule("testmodule", false)
		if err != nil {
			panic(err)
		}
		return domain.FindTranslations("da_DK")
	}

	// Translators: This is my comment to yall
	testkit.Equal(t, langkit.Get(bg, "Hello World"), "Hej til verden")
	testkit.Equal(t, langkit.Get(bg, "Hello\nBrave\nNew\nWorld"), "Hej\nFavre\nNye\nVerden")
	testkit.Equal(t, langkit.Get(bg, "It's a \"quote\""), "Det er et “citat”")
	testkit.Equal(t, langkit.GetPlural(bg, "You have one message", "You have {plural} messages", 1), "Du har en besked")
	testkit.Equal(t, langkit.GetPlural(bg, "You have one message", "You have {plural} messages", 2), "Du har 2 beskeder")
	testkit.Equal(t, langkit.GetPlural(bg, "You have one message from {1}", "You have {plural} messages from {1}", 1, "Oliver"), "Oliver har sendt dig en besked")
	testkit.Equal(t, langkit.GetPlural(bg, "You have one message from {1}", "You have {plural} messages from {1}", 2, "Oliver"), "Oliver har sendt dig 2 beskeder")
}
