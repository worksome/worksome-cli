// Command introspect fetches a GraphQL schema via introspection and outputs it
// as SDL (Schema Definition Language) to stdout.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

// introspectionQuery is the standard full introspection query.
const introspectionQuery = `{
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types {
      ...FullType
    }
    directives {
      name
      description
      locations
      args {
        ...InputValue
      }
      isRepeatable
    }
  }
}

fragment FullType on __Type {
  kind
  name
  description
  fields(includeDeprecated: true) {
    name
    description
    args {
      ...InputValue
    }
    type {
      ...TypeRef
    }
    isDeprecated
    deprecationReason
  }
  inputFields {
    ...InputValue
  }
  interfaces {
    ...TypeRef
  }
  enumValues(includeDeprecated: true) {
    name
    description
    isDeprecated
    deprecationReason
  }
  possibleTypes {
    ...TypeRef
  }
}

fragment InputValue on __InputValue {
  name
  description
  type {
    ...TypeRef
  }
  defaultValue
}

fragment TypeRef on __Type {
  kind
  name
  ofType {
    kind
    name
    ofType {
      kind
      name
      ofType {
        kind
        name
        ofType {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
            }
          }
        }
      }
    }
  }
}`

// Introspection result types matching the GraphQL introspection schema.

type introspectionResponse struct {
	Data   *introspectionData `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type introspectionData struct {
	Schema introspectionSchema `json:"__schema"`
}

type introspectionSchema struct {
	QueryType        *typeName              `json:"queryType"`
	MutationType     *typeName              `json:"mutationType"`
	SubscriptionType *typeName              `json:"subscriptionType"`
	Types            []introspectionType    `json:"types"`
	Directives       []introspectionDirective `json:"directives"`
}

type typeName struct {
	Name string `json:"name"`
}

type introspectionType struct {
	Kind          string                    `json:"kind"`
	Name          string                    `json:"name"`
	Description   string                    `json:"description"`
	Fields        []introspectionField      `json:"fields"`
	InputFields   []introspectionInputValue `json:"inputFields"`
	Interfaces    []introspectionTypeRef    `json:"interfaces"`
	EnumValues    []introspectionEnumValue  `json:"enumValues"`
	PossibleTypes []introspectionTypeRef    `json:"possibleTypes"`
}

type introspectionField struct {
	Name              string                    `json:"name"`
	Description       string                    `json:"description"`
	Args              []introspectionInputValue `json:"args"`
	Type              introspectionTypeRef      `json:"type"`
	IsDeprecated      bool                      `json:"isDeprecated"`
	DeprecationReason *string                   `json:"deprecationReason"`
}

type introspectionInputValue struct {
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Type         introspectionTypeRef `json:"type"`
	DefaultValue *string              `json:"defaultValue"`
}

type introspectionEnumValue struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

type introspectionTypeRef struct {
	Kind   string                `json:"kind"`
	Name   *string               `json:"name"`
	OfType *introspectionTypeRef `json:"ofType"`
}

type introspectionDirective struct {
	Name         string                    `json:"name"`
	Description  string                    `json:"description"`
	Locations    []string                  `json:"locations"`
	Args         []introspectionInputValue `json:"args"`
	IsRepeatable bool                      `json:"isRepeatable"`
}

// builtinScalars are the GraphQL built-in scalar types that should not be
// emitted in the SDL output since they are implicit.
var builtinScalars = map[string]bool{
	"String":  true,
	"Int":     true,
	"Float":   true,
	"Boolean": true,
	"ID":      true,
}

// builtinDirectives are the GraphQL built-in directives.
var builtinDirectives = map[string]bool{
	"skip":       true,
	"include":    true,
	"deprecated": true,
	"specifiedBy": true,
}

func main() {
	endpoint := flag.String("endpoint", "https://api.worksome.com/graphql", "GraphQL endpoint URL")
	token := flag.String("token", "", "API bearer token (or set WORKSOME_API_TOKEN)")
	flag.Parse()

	apiToken := *token
	if apiToken == "" {
		apiToken = os.Getenv("WORKSOME_API_TOKEN")
	}
	if apiToken == "" {
		fmt.Fprintln(os.Stderr, "Error: API token required. Set WORKSOME_API_TOKEN or pass --token")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Fetching schema from %s...\n", *endpoint)

	result, err := fetchIntrospection(*endpoint, apiToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching introspection: %v\n", err)
		os.Exit(1)
	}

	schema := buildSchema(result)

	var buf bytes.Buffer
	f := formatter.NewFormatter(&buf, formatter.WithIndent("  "))
	f.FormatSchema(schema)

	fmt.Print(buf.String())
	fmt.Fprintf(os.Stderr, "Schema written successfully (%d bytes)\n", buf.Len())
}

func fetchIntrospection(endpoint, token string) (*introspectionSchema, error) {
	reqBody, err := json.Marshal(map[string]string{
		"query": introspectionQuery,
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result introspectionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(result.Errors) > 0 {
		var msgs []string
		for _, e := range result.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("GraphQL errors: %s", strings.Join(msgs, "; "))
	}

	if result.Data == nil {
		return nil, fmt.Errorf("no data in response")
	}

	return &result.Data.Schema, nil
}

func buildSchema(s *introspectionSchema) *ast.Schema {
	schema := &ast.Schema{
		Types:      make(map[string]*ast.Definition),
		Directives: make(map[string]*ast.DirectiveDefinition),
	}

	// Build type definitions.
	for _, t := range s.Types {
		// Skip introspection types (prefixed with __).
		if strings.HasPrefix(t.Name, "__") {
			continue
		}
		// Skip built-in scalars.
		if t.Kind == "SCALAR" && builtinScalars[t.Name] {
			continue
		}

		def := buildDefinition(&t)
		schema.Types[t.Name] = def

		// Set root types.
		if s.QueryType != nil && t.Name == s.QueryType.Name {
			schema.Query = def
		}
		if s.MutationType != nil && t.Name == s.MutationType.Name {
			schema.Mutation = def
		}
		if s.SubscriptionType != nil && t.Name == s.SubscriptionType.Name {
			schema.Subscription = def
		}
	}

	// Build directive definitions.
	for _, d := range s.Directives {
		if builtinDirectives[d.Name] {
			continue
		}
		schema.Directives[d.Name] = buildDirectiveDefinition(&d)
	}

	return schema
}

func buildDefinition(t *introspectionType) *ast.Definition {
	def := &ast.Definition{
		Name:        t.Name,
		Description: t.Description,
		Kind:        kindToDefinitionKind(t.Kind),
	}

	// Interfaces implemented by this type.
	for _, iface := range t.Interfaces {
		if iface.Name != nil {
			def.Interfaces = append(def.Interfaces, *iface.Name)
		}
	}

	// Fields (for objects and interfaces).
	for _, f := range t.Fields {
		fieldDef := buildFieldDefinition(&f)
		def.Fields = append(def.Fields, fieldDef)
	}

	// Input fields (for input objects).
	for _, f := range t.InputFields {
		fieldDef := &ast.FieldDefinition{
			Name:        f.Name,
			Description: f.Description,
			Type:        buildType(&f.Type),
		}
		if f.DefaultValue != nil {
			fieldDef.DefaultValue = parseDefaultValue(*f.DefaultValue)
		}
		def.Fields = append(def.Fields, fieldDef)
	}

	// Enum values.
	for _, ev := range t.EnumValues {
		enumVal := &ast.EnumValueDefinition{
			Name:        ev.Name,
			Description: ev.Description,
		}
		if ev.IsDeprecated {
			enumVal.Directives = append(enumVal.Directives, buildDeprecatedDirective(ev.DeprecationReason))
		}
		def.EnumValues = append(def.EnumValues, enumVal)
	}

	// Union member types.
	for _, pt := range t.PossibleTypes {
		if t.Kind == "UNION" && pt.Name != nil {
			def.Types = append(def.Types, *pt.Name)
		}
	}

	return def
}

func buildFieldDefinition(f *introspectionField) *ast.FieldDefinition {
	fieldDef := &ast.FieldDefinition{
		Name:        f.Name,
		Description: f.Description,
		Type:        buildType(&f.Type),
	}

	for _, arg := range f.Args {
		argDef := &ast.ArgumentDefinition{
			Name:        arg.Name,
			Description: arg.Description,
			Type:        buildType(&arg.Type),
		}
		if arg.DefaultValue != nil {
			argDef.DefaultValue = parseDefaultValue(*arg.DefaultValue)
		}
		fieldDef.Arguments = append(fieldDef.Arguments, argDef)
	}

	if f.IsDeprecated {
		fieldDef.Directives = append(fieldDef.Directives, buildDeprecatedDirective(f.DeprecationReason))
	}

	return fieldDef
}

func buildDirectiveDefinition(d *introspectionDirective) *ast.DirectiveDefinition {
	def := &ast.DirectiveDefinition{
		Name:         d.Name,
		Description:  d.Description,
		IsRepeatable: d.IsRepeatable,
	}

	for _, loc := range d.Locations {
		def.Locations = append(def.Locations, ast.DirectiveLocation(loc))
	}

	for _, arg := range d.Args {
		argDef := &ast.ArgumentDefinition{
			Name:        arg.Name,
			Description: arg.Description,
			Type:        buildType(&arg.Type),
		}
		if arg.DefaultValue != nil {
			argDef.DefaultValue = parseDefaultValue(*arg.DefaultValue)
		}
		def.Arguments = append(def.Arguments, argDef)
	}

	return def
}

func buildType(ref *introspectionTypeRef) *ast.Type {
	switch ref.Kind {
	case "NON_NULL":
		if ref.OfType == nil {
			return &ast.Type{NamedType: "Unknown", NonNull: true}
		}
		inner := buildType(ref.OfType)
		inner.NonNull = true
		return inner
	case "LIST":
		if ref.OfType == nil {
			return &ast.Type{Elem: &ast.Type{NamedType: "Unknown"}}
		}
		return &ast.Type{
			Elem: buildType(ref.OfType),
		}
	default:
		name := "Unknown"
		if ref.Name != nil {
			name = *ref.Name
		}
		return &ast.Type{NamedType: name}
	}
}

func buildDeprecatedDirective(reason *string) *ast.Directive {
	dir := &ast.Directive{Name: "deprecated"}
	if reason != nil && *reason != "" && *reason != "No longer supported" {
		dir.Arguments = ast.ArgumentList{
			{
				Name: "reason",
				Value: &ast.Value{
					Raw:  *reason,
					Kind: ast.StringValue,
				},
			},
		}
	}
	return dir
}

// parseDefaultValue parses a default value string from introspection into an
// ast.Value. Introspection returns default values as their string
// representations (e.g. "null", "true", "42", `"hello"`, "[1, 2]", etc.).
func parseDefaultValue(raw string) *ast.Value {
	raw = strings.TrimSpace(raw)

	switch {
	case raw == "null":
		return &ast.Value{Raw: "null", Kind: ast.NullValue}
	case raw == "true" || raw == "false":
		return &ast.Value{Raw: raw, Kind: ast.BooleanValue}
	case isNumeric(raw):
		if strings.Contains(raw, ".") {
			return &ast.Value{Raw: raw, Kind: ast.FloatValue}
		}
		return &ast.Value{Raw: raw, Kind: ast.IntValue}
	case strings.HasPrefix(raw, `"`):
		// Unquote the string value.
		unquoted := raw[1:]
		if strings.HasSuffix(unquoted, `"`) {
			unquoted = unquoted[:len(unquoted)-1]
		}
		return &ast.Value{Raw: unquoted, Kind: ast.StringValue}
	case strings.HasPrefix(raw, "["):
		// List values — store as raw enum-like value so it serializes correctly.
		return &ast.Value{Raw: raw, Kind: ast.EnumValue}
	case strings.HasPrefix(raw, "{"):
		// Object values.
		return &ast.Value{Raw: raw, Kind: ast.EnumValue}
	default:
		// Likely an enum value.
		return &ast.Value{Raw: raw, Kind: ast.EnumValue}
	}
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	hasDot := false
	for i := start; i < len(s); i++ {
		if s[i] == '.' {
			if hasDot {
				return false
			}
			hasDot = true
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return start < len(s)
}

func kindToDefinitionKind(kind string) ast.DefinitionKind {
	switch kind {
	case "SCALAR":
		return ast.Scalar
	case "OBJECT":
		return ast.Object
	case "INTERFACE":
		return ast.Interface
	case "UNION":
		return ast.Union
	case "ENUM":
		return ast.Enum
	case "INPUT_OBJECT":
		return ast.InputObject
	default:
		return ast.Object
	}
}

