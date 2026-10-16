package domain

import (
	"errors"
	"fmt"
	"testing"
)

func TestKindOf(t *testing.T) {
	inner := NotFound("app %s not found", "web")
	if KindOf(inner) != KindNotFound || inner.Error() != "app web not found" {
		t.Errorf("NotFound: kind=%v msg=%q", KindOf(inner), inner.Error())
	}
	if KindOf(fmt.Errorf("read quotas: %w", inner)) != KindNotFound {
		t.Error("wrapping must keep the kind")
	}
	if KindOf(errors.New("db down")) != KindInternal {
		t.Error("an untagged error is internal")
	}
	if KindOf(errors.Join(Precondition("a"), Invalid("b"))) != KindPrecondition {
		t.Error("a joined error takes the kind of its first tagged error")
	}
}
