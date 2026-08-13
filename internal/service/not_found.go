package service

import (
	"errors"
	"fmt"
	"os"
)

func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, errNotFound) || errors.Is(err, os.ErrNotExist))
}

var errNotFound = errors.New("not found")

func notFoundf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errNotFound, fmt.Sprintf(format, args...))
}
