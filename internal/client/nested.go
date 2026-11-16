package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// nestedPageSize is the page size asked for on nested paginated lists. Without
// it the server's default of 10 applies, and a row's eleventh item is lost.
const nestedPageSize = 100

// maxNestedPage caps the follow-up requests that complete nested lists: each
// one re-sends the whole query, asking every nested list for its next page.
const maxNestedPage = 50

// nestedPlan is a read query rewritten so its nested paginated lists can be
// completed.
type nestedPlan struct {
	query string
	shape operationShape
	roots []*selection
	// lists are the nested paginated selections, each with the
	// paginatorInfo keys the rewrite added and must strip from the output.
	lists map[*selection][]string
	// base holds each list's head before a page argument is added.
	base map[*selection]string
}

// planNestedPages rewrites query so that every nested paginated list asks for
// nestedPageSize items and reports hasMorePages and total. It returns nil for
// mutations, for queries without nested lists, and for queries it cannot parse.
func planNestedPages(query string) *nestedPlan {
	if !isQuery(query) {
		return nil
	}
	open := operationSelectionStart(query)
	if open < 0 {
		return nil
	}
	roots, end, err := parseSelectionSet(query, open)
	if err != nil {
		return nil
	}
	plan := &nestedPlan{roots: roots, lists: map[*selection][]string{}, base: map[*selection]string{}}
	for _, r := range roots {
		plan.mark(r.children)
	}
	if len(plan.lists) == 0 {
		return nil
	}
	plan.shape = operationShape{query: query, open: open, end: end}
	plan.query = plan.render(0)
	return plan
}

// mark records the paginated lists below the operation's root fields.
func (p *nestedPlan) mark(sels []*selection) {
	for _, s := range sels {
		info, data := paginatorParts(s)
		if info != nil && data != nil && !hasArg(s.head, "page") {
			var added []string
			for _, key := range []string{"hasMorePages", "total"} {
				if findSelection(info.children, key) == nil {
					info.children = append(info.children, &selection{head: key, key: key})
					added = append(added, key)
				}
			}
			p.lists[s] = added
			p.base[s] = withArg(s.head, "first", fmt.Sprint(nestedPageSize))
		}
		p.mark(s.children)
	}
}

// render returns the query with every nested list on the given page; page 0
// leaves the page argument out.
func (p *nestedPlan) render(page int) string {
	for s, head := range p.base {
		s.head = head
		if page > 0 {
			s.head = withArg(head, "page", fmt.Sprint(page))
		}
	}
	parts := make([]string, len(p.roots))
	for i, r := range p.roots {
		parts[i] = r.render()
	}
	q := p.shape.query
	return q[:p.shape.open] + "{ " + strings.Join(parts, " ") + " }" + q[p.shape.end:]
}

// paginatorParts returns the paginatorInfo and data selections of a paginated
// list, or nils when s is not one.
func paginatorParts(s *selection) (info, data *selection) {
	if s.key == "" {
		return nil, nil
	}
	info = findSelection(s.children, "paginatorInfo")
	data = findSelection(s.children, "data")
	if info == nil || data == nil || len(info.children) == 0 || len(data.children) == 0 {
		return nil, nil
	}
	return info, data
}

func hasArg(head, name string) bool {
	_, args, ok := strings.Cut(head, "(")
	if !ok {
		return false
	}
	for _, arg := range strings.Split(strings.TrimSuffix(strings.TrimSpace(args), ")"), ",") {
		if n, _, _ := strings.Cut(arg, ":"); strings.TrimSpace(n) == name {
			return true
		}
	}
	return false
}

// withArg adds name: value to a field head, unless the head already passes it.
func withArg(head, name, value string) string {
	if hasArg(head, name) {
		return head
	}
	if i := strings.LastIndex(head, ")"); i >= 0 {
		return head[:i] + ", " + name + ": " + value + head[i:]
	}
	return head + "(" + name + ": " + value + ")"
}

// nestedList is one occurrence of a nested paginated list in a response.
type nestedList struct {
	path   []any
	sel    *selection
	list   map[string]any
	parent map[string]any
	// inner marks a list inside another nested list's items. Its parent row
	// moves between pages of the outer list, so it cannot be completed.
	inner bool
}

// collectLists walks data alongside the plan's selections and returns every
// nested paginated list in it, keyed by response path.
func (p *nestedPlan) collectLists(data any) map[string]*nestedList {
	out := map[string]*nestedList{}
	m, _ := data.(map[string]any)
	p.walk(m, p.roots, nil, false, out)
	return out
}

func (p *nestedPlan) walk(obj map[string]any, sels []*selection, path []any, inner bool, out map[string]*nestedList) {
	if obj == nil {
		return
	}
	for _, s := range sels {
		if s.key == "" {
			p.walk(obj, s.children, path, inner, out)
			continue
		}
		val := obj[s.key]
		here := append(append([]any(nil), path...), s.key)
		if _, ok := p.lists[s]; ok {
			list, _ := val.(map[string]any)
			if list == nil {
				continue
			}
			out[formatPath(here)] = &nestedList{path: here, sel: s, list: list, parent: obj, inner: inner}
			_, data := paginatorParts(s)
			items, _ := list["data"].([]any)
			for i, item := range items {
				m, _ := item.(map[string]any)
				p.walk(m, data.children, append(append([]any(nil), here...), "data", i), true, out)
			}
			continue
		}
		switch v := val.(type) {
		case map[string]any:
			p.walk(v, s.children, here, inner, out)
		case []any:
			for i, item := range v {
				m, _ := item.(map[string]any)
				p.walk(m, s.children, append(append([]any(nil), here...), i), inner, out)
			}
		}
	}
}

// completeNested fetches the remaining pages of every nested list the first
// response cut short, appends them, and warns on stderr about any list it
// could not complete.
func (c *Client) completeNested(ctx context.Context, plan *nestedPlan, variables map[string]any, raw json.RawMessage) (json.RawMessage, error) {
	data, err := decodeNumbers(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding data: %w", err)
	}
	lists := plan.collectLists(data)

	pending := map[string]*nestedList{}
	unfinished := map[string]string{}
	for key, l := range lists {
		if !hasMore(l.list) {
			continue
		}
		if l.inner {
			unfinished[key] = "it is inside another paginated list"
			continue
		}
		pending[key] = l
	}

	for page := 2; len(pending) > 0; page++ {
		if page > maxNestedPage {
			for key := range pending {
				unfinished[key] = fmt.Sprintf("the CLI stops after %d pages", maxNestedPage)
			}
			break
		}
		next, err := c.fetch(ctx, plan.render(page), variables)
		if err != nil {
			return nil, fmt.Errorf("fetching page %d of nested lists: %w", page, err)
		}
		nextData, err := decodeNumbers(next)
		if err != nil {
			return nil, fmt.Errorf("decoding page %d of nested lists: %w", page, err)
		}
		nextLists := plan.collectLists(nextData)
		for key, l := range pending {
			more := nextLists[key]
			if more == nil || !sameRow(l.parent, more.parent) {
				unfinished[key] = "the data changed between requests"
				delete(pending, key)
				continue
			}
			items, _ := l.list["data"].([]any)
			moreItems, _ := more.list["data"].([]any)
			l.list["data"] = append(items, moreItems...)
			if !hasMore(more.list) || len(moreItems) == 0 {
				markComplete(l.list)
				delete(pending, key)
			}
		}
	}
	keys := make([]string, 0, len(unfinished))
	for key := range unfinished {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		reason, l := unfinished[key], lists[key]
		items, _ := l.list["data"].([]any)
		info, _ := l.list["paginatorInfo"].(map[string]any)
		_, _ = fmt.Fprintf(c.warnw, "warning: %s: only %d of %v items were fetched, because %s\n", key, len(items), info["total"], reason)
	}
	for _, l := range lists {
		if info, ok := l.list["paginatorInfo"].(map[string]any); ok {
			for _, key := range plan.lists[l.sel] {
				delete(info, key)
			}
		}
	}

	out, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encoding data: %w", err)
	}
	return out, nil
}

func hasMore(list map[string]any) bool {
	info, _ := list["paginatorInfo"].(map[string]any)
	more, _ := info["hasMorePages"].(bool)
	return more
}

// markComplete makes a completed list's paginatorInfo describe what it holds.
func markComplete(list map[string]any) {
	info, _ := list["paginatorInfo"].(map[string]any)
	if info == nil {
		return
	}
	info["hasMorePages"] = false
	if _, ok := info["count"]; ok {
		items, _ := list["data"].([]any)
		info["count"] = len(items)
	}
}

// sameRow reports whether two responses' parent rows are the same record. Rows
// are matched by position, so a row inserted between requests would otherwise
// receive another row's items. Rows are compared by id when they have one,
// else by their fields minus nested lists, which hold a different page in
// each response.
func sameRow(a, b map[string]any) bool {
	idA, okA := a["id"]
	idB, okB := b["id"]
	if okA && okB {
		return fmt.Sprint(idA) == fmt.Sprint(idB)
	}
	ja, errA := json.Marshal(withoutLists(a))
	jb, errB := json.Marshal(withoutLists(b))
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func withoutLists(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if _, ok := t["paginatorInfo"]; ok {
			if _, ok := t["data"]; ok {
				return nil
			}
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = withoutLists(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = withoutLists(val)
		}
		return out
	default:
		return v
	}
}

// decodeNumbers decodes JSON keeping numbers verbatim, so re-encoding the data
// does not reformat them.
func decodeNumbers(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// formatPath renders a response path as GraphQLError.Location does.
func formatPath(path []any) string {
	jsonPath := make([]any, len(path))
	for i, seg := range path {
		if n, ok := seg.(int); ok {
			jsonPath[i] = float64(n)
			continue
		}
		jsonPath[i] = seg
	}
	return GraphQLError{Path: jsonPath}.Location()
}

// NestedPageQuery is query as sent for the given page of its nested lists, or
// query unchanged when it has none. Exported for the codegen schema guard.
func NestedPageQuery(query string, page int) string {
	plan := planNestedPages(query)
	if plan == nil {
		return query
	}
	return plan.render(page)
}
