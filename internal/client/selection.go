package client

import (
	"fmt"
	"strings"
)

// selection is a node in a parsed GraphQL selection set. head holds the raw
// text before the sub-selection ("worker", "hires(first: $first)", "... on X"),
// key holds the response key (alias or field name, empty for inline fragments).
type selection struct {
	head     string
	key      string
	children []*selection
}

func (s *selection) render() string {
	if len(s.children) == 0 {
		return s.head
	}
	parts := make([]string, 0, len(s.children))
	for _, c := range s.children {
		parts = append(parts, c.render())
	}
	return s.head + " { " + strings.Join(parts, " ") + " }"
}

// pruneQuery narrows the selection set of query to the field paths in fields,
// using dot notation for nested fields ("worker.name"). Paths are resolved
// against the same object the CLI prints: the items of a paginated result, or
// the root field of a single-object operation.
//
// Queries whose shape cannot be narrowed safely (unparseable, no selection set,
// more than one root field) are returned unchanged. Field paths that match
// nothing are an error, since they would otherwise select nothing at all.
func pruneQuery(query string, fields []string) (string, error) {
	paths, err := parseFieldPaths(fields)
	if err != nil || len(paths) == 0 {
		return query, err
	}
	shape, ok := parseOperation(query)
	if !ok {
		return query, nil
	}
	pruned, err := pruneSelections(shape.target.children, paths, "")
	if err != nil {
		return "", err
	}
	// Never send an empty selection set; ask for everything instead.
	if len(pruned) == 0 {
		return query, nil
	}
	return shape.render(pruned), nil
}

// parseFieldPaths splits --fields entries into path segments, skipping blanks.
func parseFieldPaths(fields []string) ([][]string, error) {
	paths := make([][]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		segments := strings.Split(f, ".")
		for _, seg := range segments {
			if strings.TrimSpace(seg) == "" {
				return nil, fmt.Errorf("--fields: %q has an empty segment", f)
			}
		}
		paths = append(paths, segments)
	}
	return paths, nil
}

// operationShape is a parsed single-root operation and the object the CLI
// prints from it: the items of a paginated result, or the root itself.
type operationShape struct {
	query  string
	open   int
	end    int
	root   *selection
	target *selection
}

// parseOperation parses query, reporting false when its shape cannot be
// narrowed safely: unparseable, no selection set, or more than one root field.
func parseOperation(query string) (operationShape, bool) {
	open := operationSelectionStart(query)
	if open < 0 {
		return operationShape{}, false
	}
	roots, end, err := parseSelectionSet(query, open)
	if err != nil || len(roots) != 1 || len(roots[0].children) == 0 {
		return operationShape{}, false
	}
	root := roots[0]
	// Paginated results wrap the items in "data"; keep the wrapper and
	// paginatorInfo intact and narrow the items instead. An object with its
	// own field called data has no paginatorInfo beside it.
	target := root
	if data := findSelection(root.children, "data"); data != nil && len(data.children) > 0 &&
		findSelection(root.children, "paginatorInfo") != nil {
		target = data
	}
	return operationShape{query: query, open: open, end: end, root: root, target: target}, true
}

// render returns the query with the target's selections replaced by sels.
func (o operationShape) render(sels []*selection) string {
	narrowed := &selection{head: o.target.head, children: sels}
	if o.target != o.root {
		children := make([]*selection, 0, len(o.root.children))
		for _, c := range o.root.children {
			if c == o.target {
				c = narrowed
			}
			children = append(children, c)
		}
		narrowed = &selection{head: o.root.head, children: children}
	}
	return o.query[:o.open] + "{ " + narrowed.render() + " }" + o.query[o.end:]
}

// pruneSelections keeps only the selections matching paths. prefix is the dotted
// path of the parent, used for error messages.
func pruneSelections(sels []*selection, paths [][]string, prefix string) ([]*selection, error) {
	order, groups := groupPaths(paths)
	matched := make(map[string]bool, len(groups))

	// Inline fragments are transparent: their selections belong to the parent.
	var pick func([]*selection) ([]*selection, error)
	pick = func(list []*selection) ([]*selection, error) {
		var kept []*selection
		for _, node := range list {
			if node.key == "" {
				inner, err := pick(node.children)
				if err != nil {
					return nil, err
				}
				if len(inner) > 0 {
					kept = append(kept, &selection{head: node.head, children: inner})
				}
				continue
			}
			tails, ok := groups[node.key]
			if !ok {
				continue
			}
			matched[node.key] = true
			if hasLeaf(tails) {
				kept = append(kept, node)
				continue
			}
			if len(node.children) == 0 {
				return nil, fmt.Errorf("--fields: %q has no subfields", prefix+node.key)
			}
			inner, err := pruneSelections(node.children, tails, prefix+node.key+".")
			if err != nil {
				return nil, err
			}
			kept = append(kept, &selection{head: node.head, children: inner})
		}
		return kept, nil
	}

	kept, err := pick(sels)
	if err != nil {
		return nil, err
	}
	for _, name := range order {
		if !matched[name] {
			return nil, fmt.Errorf("--fields: unknown field %q (available: %s)", prefix+name, formatAvailable(availableNames(sels)))
		}
	}
	return kept, nil
}

// hasLeaf reports whether any path ends here, meaning the whole subtree is kept.
func hasLeaf(tails [][]string) bool {
	for _, t := range tails {
		if len(t) == 0 {
			return true
		}
	}
	return false
}

// availableNames lists the selectable field names at a level, looking through
// inline fragments.
// maxSuggestedNames caps the field list in an unknown-field error. A hire has
// 70 selectable fields; printing all of them buries the typo.
const maxSuggestedNames = 12

// formatAvailable renders the selectable field names, truncated.
func formatAvailable(names []string) string {
	if len(names) <= maxSuggestedNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, ... and %d more", strings.Join(names[:maxSuggestedNames], ", "), len(names)-maxSuggestedNames)
}

func availableNames(sels []*selection) []string {
	var names []string
	for _, s := range sels {
		if s.key == "" {
			names = append(names, availableNames(s.children)...)
			continue
		}
		names = append(names, s.key)
	}
	return names
}

// operationSelectionStart returns the index of the "{" opening the operation's
// selection set, or -1 when there is none.
func operationSelectionStart(query string) int {
	for i := 0; i < len(query); i++ {
		switch query[i] {
		case '"':
			end, err := skipString(query, i)
			if err != nil {
				return -1
			}
			i = end - 1
		case '(':
			end, err := skipParens(query, i)
			if err != nil {
				return -1
			}
			i = end - 1
		case '{':
			return i
		}
	}
	return -1
}

// parseSelectionSet parses the selection set starting at query[open] == '{' and
// returns its selections plus the index just past the closing brace.
func parseSelectionSet(query string, open int) ([]*selection, int, error) {
	if open >= len(query) || query[open] != '{' {
		return nil, 0, fmt.Errorf("expected '{' at %d", open)
	}

	i := open + 1
	var out []*selection
	for {
		i = skipIgnored(query, i)
		if i >= len(query) {
			return nil, 0, fmt.Errorf("unterminated selection set")
		}
		if query[i] == '}' {
			return out, i + 1, nil
		}

		node := &selection{}
		start := i
		switch {
		case strings.HasPrefix(query[i:], "..."):
			i = skipIgnored(query, i+3)
			if !strings.HasPrefix(query[i:], "on") {
				return nil, 0, fmt.Errorf("unsupported fragment spread at %d", start)
			}
			i = skipIgnored(query, i+2)
			var cond string
			if cond, i = readName(query, i); cond == "" {
				return nil, 0, fmt.Errorf("missing type condition at %d", start)
			}
			node.head = strings.Join(strings.Fields(query[start:i]), " ")
		case isNameStart(query[i]):
			var name string
			name, i = readName(query, i)
			node.key = name
			if j := skipIgnored(query, i); j < len(query) && query[j] == ':' {
				j = skipIgnored(query, j+1)
				if j >= len(query) || !isNameStart(query[j]) {
					return nil, 0, fmt.Errorf("missing field name after alias at %d", start)
				}
				_, i = readName(query, j)
			}
			if j := skipIgnored(query, i); j < len(query) && query[j] == '(' {
				end, err := skipParens(query, j)
				if err != nil {
					return nil, 0, err
				}
				i = end
			}
			node.head = strings.TrimSpace(query[start:i])
		default:
			return nil, 0, fmt.Errorf("unexpected character %q at %d", query[i], i)
		}

		if j := skipIgnored(query, i); j < len(query) && query[j] == '{' {
			children, next, err := parseSelectionSet(query, j)
			if err != nil {
				return nil, 0, err
			}
			node.children = children
			i = next
		}
		out = append(out, node)
	}
}

func skipIgnored(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r', ',':
			i++
		default:
			return i
		}
	}
	return i
}

// skipParens returns the index just past the ')' matching s[i] == '('.
func skipParens(s string, i int) (int, error) {
	depth := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case '"':
			end, err := skipString(s, i)
			if err != nil {
				return 0, err
			}
			i = end - 1
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("unbalanced parentheses")
}

// skipString returns the index just past the string literal starting at s[i].
func skipString(s string, i int) (int, error) {
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string")
}

func isNameStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func readName(s string, i int) (string, int) {
	j := i
	for j < len(s) && (isNameStart(s[j]) || (s[j] >= '0' && s[j] <= '9')) {
		j++
	}
	return s[i:j], j
}
