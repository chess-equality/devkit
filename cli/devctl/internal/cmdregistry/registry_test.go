package cmdregistry

import "testing"

func TestRegistryRegisterLookup(t *testing.T) {
	r := New()
	hit := false
	r.Register("sample", func(ctx *Context) error {
		hit = true
		if ctx.Project != "foo" {
			t.Fatalf("unexpected project %q", ctx.Project)
		}
		return nil
	})
	ctx := &Context{Project: "foo"}
	h, ok := r.Lookup("sample")
	if !ok {
		t.Fatalf("handler not found")
	}
	if err := h(ctx); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !hit {
		t.Fatalf("handler was not invoked")
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	r := New()
	r.Register("dup", func(*Context) error { return nil })
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on duplicate register")
		}
	}()
	r.Register("dup", func(*Context) error { return nil })
}
