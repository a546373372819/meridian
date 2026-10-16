package domain

import (
	"errors"
	"fmt"
)


type ErrorKind int

const (
	KindInternal     ErrorKind = iota // unexpected failure
	KindInvalid                       // the request itself is wrong
	KindNotFound                      // something the request names does not exist
	KindPrecondition                  // a valid request the current state does not allow
)

type kindError struct {
	kind ErrorKind
	err  error
}

func (e *kindError) Error() string { return e.err.Error() }
func (e *kindError) Unwrap() error { return e.err }


func Invalid(format string, a ...any) error {
	return &kindError{KindInvalid, fmt.Errorf(format, a...)}
}

func NotFound(format string, a ...any) error {
	return &kindError{KindNotFound, fmt.Errorf(format, a...)}
}

func Precondition(format string, a ...any) error {
	return &kindError{KindPrecondition, fmt.Errorf(format, a...)}
}

func KindOf(err error) ErrorKind {
	var ke *kindError
	if errors.As(err, &ke) {
		return ke.kind
	}
	return KindInternal
}
