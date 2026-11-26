package client

import (
	"fmt"
	"sort"
	"strings"
)

// Schema lists the selectable fields of the API's object, interface and union
// types, keyed by type name. Generated from the vendored schema, it lets
// --fields reach any field rather than only those the default query selects.
type Schema map[string]TypeInfo

// TypeInfo describes one type in a Schema.
type TypeInfo struct {
	// Fields is nil for unions, whose members are reached through the default
	// selection's inline fragments instead.
	Fields map[string]FieldInfo
	// Nested is the selection used when --fields names the type without naming
	// any of its subfields.
	Nested string
}

// FieldInfo describes one field of a type.
type FieldInfo struct {
	// Type is the named object, interface or union type; empty for scalars and
	// enums. For a paginated relation it is the item type.
	Type      string
	Paginated bool
	// NeedsArgs marks a field that cannot be called without an argument
	// --fields has no way to pass.
	NeedsArgs bool
}

// maxFieldDepth caps how many object levels below the printed object a
// --fields path may reach, so one flag cannot fan out into an unbounded query.
const maxFieldDepth = 4

// paginatorInfoType is the schema type of a paginated relation's paginatorInfo.
const paginatorInfoType = "PaginatorInfo"

// selectFields builds the selection set for --fields against schema, starting
// at typeName: the items of a paginated result, or the root object. Fields the
// default query already selects keep their default shape; any other field on
// the type is added. Queries it cannot handle (unparseable, more than one root
// field, a union or unknown root type) fall back to pruneQuery.
func selectFields(query string, schema Schema, typeName string, fields []string) (string, error) {
	paths, err := parseFieldPaths(fields)
	if err != nil || len(paths) == 0 {
		return query, err
	}
	info, ok := schema[typeName]
	if !ok || info.Fields == nil {
		return pruneQuery(query, fields)
	}
	shape, ok := parseOperation(query)
	if !ok {
		return pruneQuery(query, fields)
	}

	kept, err := resolveFields(schema, shape.target.children, typeName, paths, "", 0, false)
	if err != nil {
		return "", err
	}
	return shape.render(kept), nil
}

// resolveFields selects paths on typeName. defaults is the default selection at
// this level, reused where it covers a path; prefix is the dotted path of the
// parent for error messages; depth counts object levels below the printed
// object; inPage reports whether a paginated relation was already crossed.
func resolveFields(schema Schema, defaults []*selection, typeName string, paths [][]string, prefix string, depth int, inPage bool) ([]*selection, error) {
	info := schema[typeName]
	if info.Fields == nil {
		// A union: its members are only reachable through the default fragments.
		return pruneSelections(defaults, paths, prefix)
	}

	order, groups := groupPaths(paths)
	kept := make([]*selection, 0, len(order))
	for _, name := range order {
		tails := groups[name]
		path := prefix + name
		def := findSelection(defaults, name)

		field, known := info.Fields[name]
		if !known {
			if def == nil {
				return nil, fmt.Errorf("--fields: unknown field %q (available: %s)", path, formatAvailable(fieldNames(info)))
			}
			// Selected by default but not a schema field, such as __typename.
			node, err := keepDefault(def, tails, path)
			if err != nil {
				return nil, err
			}
			kept = append(kept, node)
			continue
		}
		if field.NeedsArgs {
			return nil, fmt.Errorf("--fields: %q needs arguments, which --fields cannot pass", path)
		}

		if field.Type == "" {
			if hasDeeper(tails) {
				return nil, fmt.Errorf("--fields: %q has no subfields", path)
			}
			if def == nil {
				def = &selection{head: name, key: name}
			}
			kept = append(kept, def)
			continue
		}

		if depth+1 > maxFieldDepth {
			return nil, fmt.Errorf("--fields: %q is nested more than %d levels deep", path, maxFieldDepth)
		}
		if field.Paginated && inPage {
			return nil, fmt.Errorf("--fields: %q is a paginated list inside another paginated list, which --fields does not select", path)
		}

		var (
			node *selection
			err  error
		)
		if field.Paginated {
			node, err = resolvePaginated(schema, def, name, field.Type, tails, path, depth+1)
		} else {
			var children []*selection
			children, err = resolveObject(schema, childrenOf(def), field.Type, tails, path, depth+1, inPage)
			node = &selection{head: headOf(def, name), key: name, children: children}
		}
		if err != nil {
			return nil, err
		}
		kept = append(kept, node)
	}
	return withRowID(kept, info), nil
}

// withRowID adds id beside a paginated relation that has not selected it, so
// the rows its later pages are appended to can be matched across requests.
// The output filter drops it again when --fields did not name it.
func withRowID(kept []*selection, info TypeInfo) []*selection {
	if _, hasID := info.Fields["id"]; !hasID || findSelection(kept, "id") != nil {
		return kept
	}
	for _, s := range kept {
		if f, ok := info.Fields[s.key]; ok && f.Paginated {
			return append(kept, &selection{head: "id", key: "id"})
		}
	}
	return kept
}

// resolveObject selects tails on an object, interface or union field. A tail
// naming the field itself keeps the default subtree, or the type's nested
// selection when the default query does not select the field.
func resolveObject(schema Schema, defaults []*selection, typeName string, tails [][]string, path string, depth int, inPage bool) ([]*selection, error) {
	var base []*selection
	if hasLeaf(tails) {
		base = defaults
		if len(base) == 0 {
			base = parseNested(schema[typeName].Nested)
		}
		if len(base) == 0 {
			return nil, fmt.Errorf("--fields: %q is an object; name its fields, e.g. %s.id", path, path)
		}
	}
	deeper := nonEmpty(tails)
	if len(deeper) == 0 {
		return base, nil
	}
	if defaults == nil && schema[typeName].Fields == nil {
		defaults = parseNested(schema[typeName].Nested)
	}
	children, err := resolveFields(schema, defaults, typeName, deeper, path+".", depth, inPage)
	if err != nil {
		return nil, err
	}
	return mergeSelections(base, children), nil
}

// resolvePaginated selects tails on a paginated relation. Item paths may omit
// the "data" segment: "registrations.date" means "registrations.data.date".
// paginatorInfo is always kept, so the output says how many items exist.
func resolvePaginated(schema Schema, def *selection, name, itemType string, tails [][]string, path string, depth int) (*selection, error) {
	var defData, defInfo *selection
	if def != nil {
		defData = findSelection(def.children, "data")
		defInfo = findSelection(def.children, "paginatorInfo")
	}

	var itemTails, infoTails [][]string
	for _, t := range tails {
		switch {
		case len(t) > 0 && t[0] == "paginatorInfo":
			infoTails = append(infoTails, t[1:])
		case len(t) > 0 && t[0] == "data":
			itemTails = append(itemTails, t[1:])
		default:
			itemTails = append(itemTails, t)
		}
	}
	// Only paginatorInfo named: the items are not fetched at all.
	var items []*selection
	if len(itemTails) > 0 || len(infoTails) == 0 {
		if len(itemTails) == 0 {
			itemTails = [][]string{{}}
		}
		var err error
		if items, err = resolveObject(schema, childrenOf(defData), itemType, itemTails, path, depth, true); err != nil {
			return nil, err
		}
	}

	info := defInfo
	if info == nil {
		info = &selection{head: "paginatorInfo", key: "paginatorInfo", children: []*selection{{head: "total", key: "total"}}}
	}
	if deeper := nonEmpty(infoTails); len(deeper) > 0 {
		extra, err := resolveFields(schema, nil, paginatorInfoType, deeper, path+".paginatorInfo.", depth, true)
		if err != nil {
			return nil, err
		}
		info = &selection{head: info.head, key: info.key, children: mergeSelections(info.children, extra)}
	}

	children := []*selection{info}
	if len(items) > 0 {
		children = append(children, &selection{head: "data", key: "data", children: items})
	}
	return &selection{head: headOf(def, name), key: name, children: children}, nil
}

// keepDefault narrows a default selection that has no schema entry.
func keepDefault(def *selection, tails [][]string, path string) (*selection, error) {
	if hasLeaf(tails) {
		return def, nil
	}
	if len(def.children) == 0 {
		return nil, fmt.Errorf("--fields: %q has no subfields", path)
	}
	inner, err := pruneSelections(def.children, tails, path+".")
	if err != nil {
		return nil, err
	}
	return &selection{head: def.head, key: def.key, children: inner}, nil
}

// mergeSelections returns a with every selection of b it lacks, merging
// subtrees selected by both.
func mergeSelections(a, b []*selection) []*selection {
	out := append([]*selection(nil), a...)
	for _, s := range b {
		i := indexOf(out, s.key)
		switch {
		case i < 0 || s.key == "":
			out = append(out, s)
		case len(s.children) > 0:
			out[i] = &selection{head: out[i].head, key: out[i].key, children: mergeSelections(out[i].children, s.children)}
		}
	}
	return out
}

func indexOf(sels []*selection, key string) int {
	for i, s := range sels {
		if s.key == key {
			return i
		}
	}
	return -1
}

// findSelection returns the selection with the given response key at this
// level, or nil.
func findSelection(sels []*selection, key string) *selection {
	if i := indexOf(sels, key); i >= 0 {
		return sels[i]
	}
	return nil
}

func childrenOf(s *selection) []*selection {
	if s == nil {
		return nil
	}
	return s.children
}

// headOf keeps the default head, which may carry arguments.
func headOf(def *selection, name string) string {
	if def != nil {
		return def.head
	}
	return name
}

func parseNested(nested string) []*selection {
	if strings.TrimSpace(nested) == "" {
		return nil
	}
	sels, _, err := parseSelectionSet("{ "+nested+" }", 0)
	if err != nil {
		return nil
	}
	return sels
}

func fieldNames(info TypeInfo) []string {
	names := make([]string, 0, len(info.Fields))
	for name, f := range info.Fields {
		if !f.NeedsArgs {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func hasDeeper(tails [][]string) bool {
	return len(nonEmpty(tails)) > 0
}

func nonEmpty(tails [][]string) [][]string {
	var out [][]string
	for _, t := range tails {
		if len(t) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// groupPaths groups paths by their first segment, keeping first-seen order.
func groupPaths(paths [][]string) ([]string, map[string][][]string) {
	groups := make(map[string][][]string, len(paths))
	var order []string
	for _, p := range paths {
		if _, seen := groups[p[0]]; !seen {
			order = append(order, p[0])
		}
		groups[p[0]] = append(groups[p[0]], p[1:])
	}
	return order, groups
}

// SelectFields is the selection ExecuteFor sends for fields. Exported for the
// codegen guard, which validates those documents against the schema.
func SelectFields(query string, schema Schema, typeName string, fields []string) (string, error) {
	return selectFields(query, schema, typeName, fields)
}
