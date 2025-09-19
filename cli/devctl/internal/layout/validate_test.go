package layout

import (
	"strings"
	"testing"
)

func TestValidateNoIssues(t *testing.T) {
	f := File{
		Overlays: []Overlay{{Project: "dev-all", Count: 2}},
		Windows: []Window{
			{Index: 1, Project: "dev-all", Service: "dev-agent", Name: "one"},
			{Index: 2, Project: "dev-all", Service: "dev-agent", Name: "two"},
		},
	}
	warns, errs := Validate(f, "dev-all")
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateDetectsIssues(t *testing.T) {
	f := File{
		Overlays: []Overlay{{Project: "dev-all", Count: 1}},
		Windows: []Window{
			{Index: 1, Project: "dev-all", Service: "dev-agent", Name: "a"},
			{Index: 1, Project: "dev-all", Service: "dev-agent", Name: "b"},
			{Index: 2, Project: "dev-all", Service: "dev-agent", Name: "c"},
			{Index: 0, Project: "dev-all", Service: "dev-agent", Name: "bad"},
			{Index: 1, Project: "other", Service: "dev-agent", Name: "d"},
		},
	}
	warns, errs := Validate(f, "dev-all")
	expectedWarnContains := []string{
		"multiple windows target dev-all/dev-agent index 1",
		"no overlay defined for project other",
	}
	for _, expected := range expectedWarnContains {
		found := false
		for _, w := range warns {
			if strings.Contains(w, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected warning containing %q, warnings=%v", expected, warns)
		}
	}
	if len(errs) == 0 {
		t.Fatalf("expected errors, got none")
	}
	contains := func(list []string, substr string) bool {
		for _, v := range list {
			if strings.Contains(v, substr) {
				return true
			}
		}
		return false
	}
	if !contains(errs, "requires container index 2") {
		t.Fatalf("expected count error, errs=%v", errs)
	}
	if !contains(errs, "invalid index") {
		t.Fatalf("expected invalid index error, errs=%v", errs)
	}
}

func TestValidateNoDefaultProject(t *testing.T) {
	f := File{Windows: []Window{{Index: 1}}}
	warns, errs := Validate(f, "")
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "missing a project") {
		t.Fatalf("expected missing project error, errs=%v", errs)
	}
}
