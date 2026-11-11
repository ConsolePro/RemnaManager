package services

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorTextChoosesLanguageAndPreservesWrappedDiagnostics(t *testing.T) {
	err := fmt.Errorf("%w; private log: /private/install.log", LocalizedError("Завершите установку панели.", "Finish panel installation."))
	if got := ErrorText(err, "ru"); got != "Завершите установку панели.; Приватный журнал: /private/install.log" {
		t.Fatal(got)
	}
	if got := ErrorText(err, "en"); got != err.Error() {
		t.Fatal(got)
	}
	if got := ErrorText(errors.New("diagnostic"), "ru"); got != "diagnostic" {
		t.Fatal(got)
	}
	if got := ErrorText(nil, "ru"); got != "" {
		t.Fatal(got)
	}
}

func TestNestedActionErrorsAreTranslated(t *testing.T) {
	err := fmt.Errorf("%w: %w; remote log: /node/install.log", LocalizedError("Этап не завершён", "Stage failed"), LocalizedError("Не совпадает SHA256", "SHA256 mismatch"))
	if got := ErrorText(err, "ru"); got != "Этап не завершён: Не совпадает SHA256; Журнал на ноде: /node/install.log" {
		t.Fatal(got)
	}
	if got := ErrorText(err, "en"); got != err.Error() {
		t.Fatal(got)
	}
}
