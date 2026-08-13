package service

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestIsNotFoundUsesTypedWrappedErrorsOnly(t *testing.T) {
	if !IsNotFound(os.ErrNotExist) || !IsNotFound(fmt.Errorf("read record: %w", os.ErrNotExist)) {
		t.Fatal("filesystem not-found sentinel was not recognized through wrapping")
	}
	if !IsNotFound(notFoundf("task %s", "EXM-TSK1")) {
		t.Fatal("service not-found sentinel was not recognized through wrapping")
	}
	if IsNotFound(errors.New("task not found: EXM-TSK1")) {
		t.Fatal("free-form not-found text was treated as a typed sentinel")
	}
}
