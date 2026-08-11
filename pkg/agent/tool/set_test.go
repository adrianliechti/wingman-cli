package tool

import (
	"errors"
	"strings"
	"testing"
)

func TestSetClosesOwnedResourcesInReverseAndIsolatesPanics(t *testing.T) {
	set := NewSet(Tool{Name: "read"})
	var order []string
	set.Own("first", func() error {
		order = append(order, "first")
		return nil
	})
	set.Own("panicking", func() error {
		order = append(order, "panicking")
		panic("boom")
	})
	set.Own("last", func() error {
		order = append(order, "last")
		return errors.New("close failed")
	})

	err := set.Close()
	if got := strings.Join(order, ","); got != "last,panicking,first" {
		t.Fatalf("close order = %s", got)
	}
	if err == nil || !strings.Contains(err.Error(), "close failed") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("joined close error = %v", err)
	}
	if err := set.Close(); err != nil || len(order) != 3 {
		t.Fatalf("second close = %v, order = %v", err, order)
	}
}

func TestSetSliceIsIndependent(t *testing.T) {
	set := NewSet(Tool{Name: "read"})
	tools := set.Slice()
	tools[0].Name = "changed"
	if got := set.Slice()[0].Name; got != "read" {
		t.Fatalf("slice mutated set: %s", got)
	}
}
