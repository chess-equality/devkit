package layout

import (
	"fmt"
	"sort"
	"strings"
)

type containerRef struct {
	Project string
	Service string
	Index   int
}

func windowLabel(w Window) string {
	name := strings.TrimSpace(w.Name)
	if name != "" {
		return fmt.Sprintf("%s (index %d)", name, w.Index)
	}
	return fmt.Sprintf("index %d", w.Index)
}

// Validate inspects a layout file and returns warnings and errors describing potential issues.
func Validate(f File, defaultProject string) (warnings []string, errors []string) {
	defaultProject = strings.TrimSpace(defaultProject)
	overlayCounts := map[string]int{}
	for _, ov := range f.Overlays {
		proj := strings.TrimSpace(ov.Project)
		if proj == "" {
			proj = defaultProject
		}
		if proj == "" {
			continue
		}
		count := ov.Count
		if count < 1 {
			count = 1
		}
		overlayCounts[proj] = count
	}

	maxIndex := map[string]int{}
	dupMap := map[containerRef][]Window{}
	missingOverlay := map[string]bool{}

	for _, w := range f.Windows {
		idx := w.Index
		if idx < 1 {
			errors = append(errors, fmt.Sprintf("window %s uses invalid index %d", windowLabel(w), idx))
			continue
		}
		proj := strings.TrimSpace(w.Project)
		if proj == "" {
			proj = defaultProject
		}
		if proj == "" {
			errors = append(errors, fmt.Sprintf("window %s is missing a project and no default project was provided", windowLabel(w)))
			continue
		}
		svc := strings.TrimSpace(w.Service)
		if svc == "" {
			svc = "dev-agent"
		}
		ref := containerRef{Project: proj, Service: svc, Index: idx}
		dupMap[ref] = append(dupMap[ref], w)
		if idx > maxIndex[proj] {
			maxIndex[proj] = idx
		}
		if _, ok := overlayCounts[proj]; !ok {
			missingOverlay[proj] = true
		}
	}

	for proj := range missingOverlay {
		warnings = append(warnings, fmt.Sprintf("no overlay defined for project %s; assuming default compose files", proj))
	}

	for proj, maxIdx := range maxIndex {
		if count, ok := overlayCounts[proj]; ok {
			if maxIdx > count {
				errors = append(errors, fmt.Sprintf("project %s requires container index %d but overlay count is %d", proj, maxIdx, count))
			}
		}
	}

	for ref, wins := range dupMap {
		if len(wins) <= 1 {
			continue
		}
		labels := make([]string, 0, len(wins))
		for _, w := range wins {
			labels = append(labels, windowLabel(w))
		}
		sort.Strings(labels)
		warnings = append(warnings, fmt.Sprintf("multiple windows target %s/%s index %d (%s)", ref.Project, ref.Service, ref.Index, strings.Join(labels, ", ")))
	}

	sort.Strings(warnings)
	sort.Strings(errors)
	return warnings, errors
}
