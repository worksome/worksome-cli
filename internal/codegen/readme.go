package codegen

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// readmeCountMarker matches a count the README keeps in step with the schema:
// <!-- resources -->82<!-- /resources -->. The prose around it is hand-written;
// the number between the markers is not.
var readmeCountMarker = regexp.MustCompile(`(<!-- (resources|operations) -->)(\d+)(<!-- /(resources|operations) -->)`)

// OperationCount is the number of operations the generated querier exposes —
// every get, list and mutation across all resources.
func (s *Schema) OperationCount() int {
	n := 0
	for _, r := range s.Resources {
		if r.GetQuery != nil {
			n++
		}
		if r.ListQuery != nil {
			n++
		}
		n += len(r.Mutations)
	}
	return n
}

// UpdateReadmeCounts rewrites every marked count in the README at path from
// the schema, so the numbers in the prose cannot drift from what the code
// generator actually produced. It returns whether the file changed.
//
// A README with no markers is an error rather than a no-op: a guard that
// silently stops covering a number is worse than none.
func UpdateReadmeCounts(path string, schema *Schema) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	counts := map[string]int{
		"resources":  len(schema.Resources),
		"operations": schema.OperationCount(),
	}

	seen := map[string]bool{}
	var bad string
	out := readmeCountMarker.ReplaceAllStringFunc(string(src), func(m string) string {
		sub := readmeCountMarker.FindStringSubmatch(m)
		open, close := sub[2], sub[5]
		if open != close {
			bad = m
			return m
		}
		seen[open] = true
		return sub[1] + strconv.Itoa(counts[open]) + sub[4]
	})
	if bad != "" {
		return false, fmt.Errorf("%s: mismatched count markers: %s", path, bad)
	}
	for name := range counts {
		if !seen[name] {
			return false, fmt.Errorf("%s: no <!-- %s --> marker found; the README no longer tracks that count", path, name)
		}
	}
	if out == string(src) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(out), 0o644)
}
