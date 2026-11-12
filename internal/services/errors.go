package services

import (
	"strings"
)

type localizedError struct {
	ru, en string
	cause  error
}

func (e *localizedError) Error() string { return e.en }
func (e *localizedError) Unwrap() error { return e.cause }

// LocalizedError keeps the service language-neutral until the UI renders it.
func LocalizedError(ru, en string) error { return &localizedError{ru: ru, en: en} }

// WrapLocalized keeps the diagnostic cause for logs/errors.Is without forcing
// untranslated low-level diagnostics into the selected UI language.
func WrapLocalized(cause error, ru, en string) error {
	return &localizedError{ru: ru, en: en, cause: cause}
}

func ErrorText(err error, language string) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if language == "en" {
		return text
	}
	var translate func(error, int)
	translate = func(current error, depth int) {
		if current == nil || depth > 32 {
			return
		}
		if localized, ok := current.(*localizedError); ok {
			text = strings.Replace(text, localized.en, localized.ru, 1)
		}
		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				translate(child, depth+1)
			}
		case interface{ Unwrap() error }:
			translate(wrapped.Unwrap(), depth+1)
		}
	}
	translate(err, 0)
	text = strings.ReplaceAll(text, "; private log: ", "; Приватный журнал: ")
	text = strings.ReplaceAll(text, "; remote log: ", "; Журнал на ноде: ")
	return text
}
