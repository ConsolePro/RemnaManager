package services

import (
	"errors"
	"strings"
)

type localizedError struct{ ru, en string }

func (e *localizedError) Error() string { return e.en }

// LocalizedError keeps the service language-neutral until the UI renders it.
func LocalizedError(ru, en string) error { return &localizedError{ru: ru, en: en} }

func ErrorText(err error, language string) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if language == "en" {
		return text
	}
	var translated *localizedError
	if errors.As(err, &translated) {
		text = strings.Replace(text, translated.en, translated.ru, 1)
		text = strings.Replace(text, "; private log: ", "; Приватный журнал: ", 1)
	}
	return text
}
