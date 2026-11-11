package i18n

import "strings"

type localizedError struct {
	zh, en string
	cause  error
}

// NewError creates an error whose default text is Chinese.
func NewError(zh, en string) error {
	return &localizedError{zh: zh, en: en}
}

// WrapError adds bilingual context without changing the underlying error identity.
func WrapError(zh, en string, cause error) error {
	return &localizedError{zh: zh, en: en, cause: cause}
}

func (e *localizedError) Error() string {
	if e.cause == nil {
		return e.zh
	}
	return e.zh + ": " + e.cause.Error()
}

func (e *localizedError) Unwrap() error { return e.cause }

// ErrorText translates application-owned context while retaining external error text.
func ErrorText(err error, english bool) string {
	if err == nil {
		return ""
	}
	if !english {
		return err.Error()
	}
	if e, ok := err.(*localizedError); ok {
		if e.cause == nil {
			return e.en
		}
		return e.en + ": " + ErrorText(e.cause, true)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		text := err.Error()
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				text = strings.Replace(text, cause.Error(), ErrorText(cause, true), 1)
			}
		}
		return text
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if cause := wrapped.Unwrap(); cause != nil {
			return strings.Replace(err.Error(), cause.Error(), ErrorText(cause, true), 1)
		}
	}
	return err.Error()
}
