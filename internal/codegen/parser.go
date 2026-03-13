package codegen

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"gopkg.in/yaml.v3"
)

// Overrides configures manual resource grouping and operation exclusions.
type Overrides struct {
	Resources map[string]OverrideResource `yaml:"resources"`
	Ignore    []string                    `yaml:"ignore"`
}

// OverrideResource maps operations to a specific resource group.
type OverrideResource struct {
	Queries   []string `yaml:"queries"`
	Mutations []string `yaml:"mutations"`
}

// scalarMap maps GraphQL scalar types to Go types.
var scalarMap = map[string]string{
	"String":           "string",
	"Int":              "int",
	"Float":            "float64",
	"Boolean":          "bool",
	"ID":               "string",
	"DateTime":         "string",
	"Date":             "string",
	"Time":             "string",
	"Decimal":          "float64",
	"DecimalTwo":       "float64",
	"Percentage":       "float64",
	"StrictPercentage": "float64",
	"Upload":           "string",
	"Json":             "map[string]any",
	"JSON":             "map[string]any",
	"Dictionary":       "map[string]any",
	"URL":              "string",
	"E164PhoneNumber":  "string",
}

// knownScalars is the set of all scalars we recognize.
var knownScalars = func() map[string]bool {
	m := make(map[string]bool, len(scalarMap))
	for k := range scalarMap {
		m[k] = true
	}
	return m
}()

// ParseSchema parses a GraphQL schema file and optional overrides file into an IR Schema.
func ParseSchema(schemaPath, overridesPath string) (*Schema, error) {
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("reading schema: %w", err)
	}

	source := &ast.Source{
		Name:  schemaPath,
		Input: string(schemaBytes),
	}

	doc, gqlErr := gqlparser.LoadSchema(source)
	if gqlErr != nil {
		return nil, fmt.Errorf("parsing schema: %s", gqlErr.Error())
	}

	var overrides Overrides
	if overridesPath != "" {
		overridesBytes, err := os.ReadFile(overridesPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading overrides: %w", err)
		}
		if err == nil {
			if err := yaml.Unmarshal(overridesBytes, &overrides); err != nil {
				return nil, fmt.Errorf("parsing overrides: %w", err)
			}
		}
	}

	p := &parser{
		doc:       doc,
		overrides: overrides,
		enums:     make(map[string]bool),
		inputs:    make(map[string]bool),
		paginators: make(map[string]string),
	}

	return p.parse()
}

type parser struct {
	doc        *ast.Schema
	overrides  Overrides
	enums      map[string]bool
	inputs     map[string]bool
	paginators map[string]string // PaginatorTypeName -> DataTypeName
}

func (p *parser) parse() (*Schema, error) {
	schema := &Schema{}

	// First pass: identify enums, inputs, and paginators
	for name, def := range p.doc.Types {
		switch def.Kind {
		case ast.Enum:
			if isBuiltinType(name) {
				continue
			}
			p.enums[name] = true
		case ast.InputObject:
			p.inputs[name] = true
		case ast.Object:
			if strings.HasSuffix(name, "Paginator") {
				if dataField := def.Fields.ForName("data"); dataField != nil {
					innerType := unwrapType(dataField.Type)
					p.paginators[name] = innerType
				}
			}
		}
	}

	// Parse enums
	for name, def := range p.doc.Types {
		if def.Kind != ast.Enum || isBuiltinType(name) {
			continue
		}
		schema.Enums = append(schema.Enums, p.parseEnum(def))
	}
	sort.Slice(schema.Enums, func(i, j int) bool {
		return schema.Enums[i].Name < schema.Enums[j].Name
	})

	// Parse input objects
	for name, def := range p.doc.Types {
		if def.Kind != ast.InputObject || isBuiltinType(name) {
			continue
		}
		schema.Inputs = append(schema.Inputs, p.parseInputObject(def))
	}
	sort.Slice(schema.Inputs, func(i, j int) bool {
		return schema.Inputs[i].Name < schema.Inputs[j].Name
	})

	// Parse object types (skip Query, Mutation, Subscription, paginators, and builtins)
	for name, def := range p.doc.Types {
		if def.Kind != ast.Object || isBuiltinType(name) {
			continue
		}
		if name == "Query" || name == "Mutation" || name == "Subscription" {
			continue
		}
		// Keep PaginatorInfo and *Paginator types — other types reference them
		schema.Objects = append(schema.Objects, p.parseObject(def))
	}
	sort.Slice(schema.Objects, func(i, j int) bool {
		return schema.Objects[i].Name < schema.Objects[j].Name
	})

	// Parse interfaces
	for name, def := range p.doc.Types {
		if def.Kind != ast.Interface || isBuiltinType(name) {
			continue
		}
		schema.Interfaces = append(schema.Interfaces, p.parseInterface(def))
	}
	sort.Slice(schema.Interfaces, func(i, j int) bool {
		return schema.Interfaces[i].Name < schema.Interfaces[j].Name
	})

	// Parse unions
	for name, def := range p.doc.Types {
		if def.Kind != ast.Union || isBuiltinType(name) {
			continue
		}
		schema.Unions = append(schema.Unions, p.parseUnion(def))
	}
	sort.Slice(schema.Unions, func(i, j int) bool {
		return schema.Unions[i].Name < schema.Unions[j].Name
	})

	// Collect scalar names
	for name, def := range p.doc.Types {
		if def.Kind == ast.Scalar && !isBuiltinType(name) {
			schema.Scalars = append(schema.Scalars, name)
		}
	}
	sort.Strings(schema.Scalars)

	// Parse operations and group into resources
	schema.Resources = p.buildResources()

	return schema, nil
}

func (p *parser) parseEnum(def *ast.Definition) Enum {
	e := Enum{
		Name:        def.Name,
		GoName:      def.Name,
		Description: cleanDescription(def.Description),
	}
	for _, v := range def.EnumValues {
		e.Values = append(e.Values, EnumValue{
			Name:        v.Name,
			GoName:      def.Name + toPascalCase(strings.ToLower(v.Name)),
			Description: cleanDescription(v.Description),
			Deprecated:  hasDeprecated(v.Directives),
		})
	}
	return e
}

func (p *parser) parseInputObject(def *ast.Definition) InputObject {
	obj := InputObject{
		Name:        def.Name,
		GoName:      def.Name,
		Description: cleanDescription(def.Description),
	}
	for _, f := range def.Fields {
		obj.Fields = append(obj.Fields, p.parseArgument(f))
	}
	return obj
}

func (p *parser) parseArgument(f *ast.FieldDefinition) Argument {
	return Argument{
		Name:        f.Name,
		GoName:      toPascalCase(f.Name),
		CLIFlag:     toKebabCase(f.Name),
		Description: cleanDescription(f.Description),
		Type:        p.resolveType(f.Type),
		DefaultValue: defaultValueString(f.DefaultValue),
	}
}

func (p *parser) parseArgumentFromArg(a *ast.ArgumentDefinition) Argument {
	return Argument{
		Name:         a.Name,
		GoName:       toPascalCase(a.Name),
		CLIFlag:      toKebabCase(a.Name),
		Description:  cleanDescription(a.Description),
		Type:         p.resolveType(a.Type),
		DefaultValue: defaultValueString(a.DefaultValue),
	}
}

func (p *parser) parseObject(def *ast.Definition) Object {
	obj := Object{
		Name:        def.Name,
		GoName:      def.Name,
		Description: cleanDescription(def.Description),
	}
	for _, iface := range def.Interfaces {
		obj.Implements = append(obj.Implements, iface)
	}
	for _, f := range def.Fields {
		if f.Name == "__typename" {
			continue
		}
		obj.Fields = append(obj.Fields, Field{
			Name:        f.Name,
			GoName:      toPascalCase(f.Name),
			JSONName:    f.Name,
			Description: cleanDescription(f.Description),
			Type:        p.resolveType(f.Type),
			Deprecated:  hasDeprecated(f.Directives),
		})
	}
	return obj
}

func (p *parser) parseInterface(def *ast.Definition) Interface {
	iface := Interface{
		Name:        def.Name,
		GoName:      def.Name,
		Description: cleanDescription(def.Description),
	}
	for _, f := range def.Fields {
		iface.Fields = append(iface.Fields, Field{
			Name:        f.Name,
			GoName:      toPascalCase(f.Name),
			JSONName:    f.Name,
			Description: cleanDescription(f.Description),
			Type:        p.resolveType(f.Type),
		})
	}
	// Find implementors
	for name, t := range p.doc.Types {
		if t.Kind != ast.Object {
			continue
		}
		for _, impl := range t.Interfaces {
			if impl == def.Name {
				iface.Implementors = append(iface.Implementors, name)
			}
		}
	}
	sort.Strings(iface.Implementors)
	return iface
}

func (p *parser) parseUnion(def *ast.Definition) Union {
	u := Union{
		Name:   def.Name,
		GoName: def.Name,
	}
	for _, m := range def.Types {
		u.Members = append(u.Members, m)
	}
	sort.Strings(u.Members)
	return u
}

func (p *parser) resolveType(t *ast.Type) TypeRef {
	ref := TypeRef{
		IsRequired: t.NonNull,
	}

	if t.Elem != nil {
		// List type
		ref.IsList = true
		inner := p.resolveType(t.Elem)
		ref.ListItem = &inner
		ref.Name = inner.Name
		if inner.IsRequired {
			ref.GoType = fmt.Sprintf("[]%s", inner.GoType)
		} else {
			ref.GoType = fmt.Sprintf("[]*%s", inner.GoType)
		}
		if !ref.IsRequired {
			// pointer to slice — in practice we just use the slice (nil slice is fine)
			// keep GoType as-is
		}
		return ref
	}

	ref.Name = t.NamedType

	// Check if paginator
	if innerType, ok := p.paginators[t.NamedType]; ok {
		ref.IsPaginator = true
		ref.PaginatedType = innerType
		ref.GoType = t.NamedType
		return ref
	}

	// Check scalar
	if goType, ok := scalarMap[t.NamedType]; ok {
		ref.IsScalar = true
		if ref.IsRequired {
			ref.GoType = goType
		} else {
			ref.GoType = ptrType(goType)
		}
		return ref
	}

	// Check enum
	if p.enums[t.NamedType] {
		ref.IsEnum = true
		if ref.IsRequired {
			ref.GoType = t.NamedType
		} else {
			ref.GoType = "*" + t.NamedType
		}
		return ref
	}

	// Check input
	if p.inputs[t.NamedType] {
		ref.IsInput = true
		if ref.IsRequired {
			ref.GoType = t.NamedType
		} else {
			ref.GoType = "*" + t.NamedType
		}
		return ref
	}

	// Object/interface/union type — always use pointer to avoid recursive type issues
	ref.GoType = "*" + t.NamedType
	return ref
}

// buildResources groups queries and mutations into resource groups.
func (p *parser) buildResources() []Resource {
	ignored := make(map[string]bool)
	for _, name := range p.overrides.Ignore {
		ignored[name] = true
	}

	// Collect all queries and mutations
	queries := make(map[string]*ast.FieldDefinition)
	mutations := make(map[string]*ast.FieldDefinition)

	if q := p.doc.Types["Query"]; q != nil {
		for _, f := range q.Fields {
			if !ignored[f.Name] {
				queries[f.Name] = f
			}
		}
	}
	if m := p.doc.Types["Mutation"]; m != nil {
		for _, f := range m.Fields {
			if !ignored[f.Name] {
				mutations[f.Name] = f
			}
		}
	}

	// Track which operations are claimed by overrides
	claimed := make(map[string]bool)
	resourceMap := make(map[string]*Resource)

	// Process overrides first
	for name, override := range p.overrides.Resources {
		res := &Resource{
			Name:   name,
			GoName: toPascalCase(name),
		}
		for _, qName := range override.Queries {
			if f, ok := queries[qName]; ok {
				op := p.fieldToOperation(f, OperationQuery, name)
				retType := resolveReturnTypeName(f.Type)
				if _, isPag := p.paginators[retType]; isPag {
					op.CLIName = "list"
					res.ListQuery = &op
				} else {
					op.CLIName = "get"
					res.GetQuery = &op
				}
				claimed[qName] = true
			}
		}
		for _, mName := range override.Mutations {
			if f, ok := mutations[mName]; ok {
				op := p.fieldToOperation(f, OperationMutation, name)
				res.Mutations = append(res.Mutations, op)
				claimed[mName] = true
			}
		}
		resourceMap[name] = res
	}

	// Auto-detect resource groups from queries
	// Look for singular/plural pairs: hire/hires, job/jobs
	singularQueries := make(map[string]*ast.FieldDefinition)
	pluralQueries := make(map[string]*ast.FieldDefinition)

	for name, f := range queries {
		if claimed[name] {
			continue
		}
		retType := resolveReturnTypeName(f.Type)
		if _, isPag := p.paginators[retType]; isPag {
			pluralQueries[name] = f
		} else if p.isSingularGet(f) {
			singularQueries[name] = f
		}
	}

	// Match plural queries to resource names
	for pluralName, pluralField := range pluralQueries {
		resourceName := toKebabCase(pluralName)
		singularName := toSingular(pluralName)

		res, exists := resourceMap[resourceName]
		if !exists {
			res = &Resource{
				Name:   resourceName,
				GoName: toPascalCase(pluralName),
			}
			resourceMap[resourceName] = res
		}

		op := p.fieldToOperation(pluralField, OperationQuery, resourceName)
		op.CLIName = "list"
		res.ListQuery = &op
		res.Description = cleanDescription(pluralField.Description)
		claimed[pluralName] = true

		// Try to find matching singular
		if singField, ok := singularQueries[singularName]; ok {
			getOp := p.fieldToOperation(singField, OperationQuery, resourceName)
			getOp.CLIName = "get"
			res.GetQuery = &getOp
			claimed[singularName] = true
		}
	}

	// Handle remaining singular queries that don't have a plural counterpart
	for name, f := range singularQueries {
		if claimed[name] {
			continue
		}
		resourceName := toKebabCase(name)
		res, exists := resourceMap[resourceName]
		if !exists {
			res = &Resource{
				Name:   resourceName,
				GoName: toPascalCase(name),
			}
			resourceMap[resourceName] = res
		}
		getOp := p.fieldToOperation(f, OperationQuery, resourceName)
		getOp.CLIName = "get"
		res.GetQuery = &getOp
		res.Description = cleanDescription(f.Description)
		claimed[name] = true
	}

	// Handle remaining unclaimed queries (like 'accounts', 'viewer')
	for name, f := range queries {
		if claimed[name] {
			continue
		}
		resourceName := toKebabCase(name)
		res, exists := resourceMap[resourceName]
		if !exists {
			res = &Resource{
				Name:   resourceName,
				GoName: toPascalCase(name),
			}
			resourceMap[resourceName] = res
		}
		op := p.fieldToOperation(f, OperationQuery, resourceName)
		// If no arguments, it's a "get" style (like viewer)
		if len(f.Arguments) == 0 {
			op.CLIName = "get"
			res.GetQuery = &op
		} else {
			op.CLIName = "list"
			res.ListQuery = &op
		}
		res.Description = cleanDescription(f.Description)
		claimed[name] = true
	}

	// Match mutations to resources
	for name, f := range mutations {
		if claimed[name] {
			continue
		}
		resourceName := p.matchMutationToResource(name, resourceMap)
		res, exists := resourceMap[resourceName]
		if !exists {
			res = &Resource{
				Name:   resourceName,
				GoName: toPascalCase(resourceName),
			}
			resourceMap[resourceName] = res
		}
		op := p.fieldToOperation(f, OperationMutation, resourceName)
		op.CLIName = p.deriveMutationCLIName(name, resourceName)
		res.Mutations = append(res.Mutations, op)
		claimed[name] = true
	}

	// Convert map to sorted slice
	var resources []Resource
	for _, res := range resourceMap {
		if res.GetQuery == nil && res.ListQuery == nil && len(res.Mutations) == 0 {
			continue
		}
		sort.Slice(res.Mutations, func(i, j int) bool {
			return res.Mutations[i].Name < res.Mutations[j].Name
		})
		resources = append(resources, *res)
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Name < resources[j].Name
	})

	return resources
}

func (p *parser) fieldToOperation(f *ast.FieldDefinition, opType OperationType, resourceName string) Operation {
	op := Operation{
		Name:        f.Name,
		GoName:      toPascalCase(f.Name),
		Type:        opType,
		Description: cleanDescription(f.Description),
		ReturnType:  p.resolveType(f.Type),
	}

	for _, arg := range f.Arguments {
		op.Arguments = append(op.Arguments, p.parseArgumentFromArg(arg))
	}

	if dir := getDirective(f.Directives, "deprecated"); dir != nil {
		op.Deprecated = true
		if reason := getDirectiveArg(dir, "reason"); reason != "" {
			op.DeprecatedReason = reason
		}
	}

	// Build selection set from return type
	op.SelectionSet = p.buildSelectionSet(f.Type, 1)

	// For mutations with a single input argument, resolve the input type's fields as CLI flags
	if opType == OperationMutation {
		p.resolveInputFields(&op)
	}

	return op
}

// resolveInputFields introspects the input type for mutations that take a single `input: SomeInput!`
// argument, and populates InputFields with scalar/enum/ID fields that can be exposed as CLI flags.
func (p *parser) resolveInputFields(op *Operation) {
	if len(op.Arguments) != 1 {
		return
	}
	arg := op.Arguments[0]
	if arg.Name != "input" || !arg.Type.IsInput {
		return
	}

	inputDef := p.doc.Types[arg.Type.Name]
	if inputDef == nil || inputDef.Kind != ast.InputObject {
		return
	}

	op.InputTypeName = arg.Type.Name

	for _, f := range inputDef.Fields {
		ref := p.resolveType(f.Type)
		// Only expose scalar, enum, and ID fields as CLI flags.
		// Skip nested input objects, lists of inputs, and other complex types.
		if ref.IsScalar || ref.IsEnum {
			op.InputFields = append(op.InputFields, Argument{
				Name:         f.Name,
				GoName:       toPascalCase(f.Name),
				CLIFlag:      toKebabCase(f.Name),
				Description:  cleanDescription(f.Description),
				Type:         ref,
				DefaultValue: defaultValueString(f.DefaultValue),
			})
		}
	}
}

// buildSelectionSet generates a GraphQL selection set for a type, selecting all scalar
// fields and one level of nested object fields (scalar-only).
func (p *parser) buildSelectionSet(t *ast.Type, depth int) string {
	typeName := unwrapType(t)

	// Check if it's a paginator — select data fields + paginatorInfo
	if innerType, ok := p.paginators[typeName]; ok {
		innerDef := p.doc.Types[innerType]
		if innerDef == nil {
			return "{ paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { id } }"
		}
		dataFields := p.selectScalarFields(innerDef, 1)
		return "{ paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { " + dataFields + " } }"
	}

	// Check if it's a known object type
	def := p.doc.Types[typeName]
	if def == nil || def.Kind != ast.Object {
		return ""
	}

	fields := p.selectScalarFields(def, depth)
	if fields == "" {
		return "{ id }"
	}
	return "{ " + fields + " }"
}

// selectScalarFields returns a space-separated list of scalar field selections for a type.
// At depth > 0, it also includes one level of nested object scalar fields.
func (p *parser) selectScalarFields(def *ast.Definition, depth int) string {
	var fields []string
	for _, f := range def.Fields {
		if f.Name == "__typename" {
			continue
		}
		innerType := unwrapType(f.Type)

		// Scalar or enum field — always include
		if knownScalars[innerType] || p.enums[innerType] {
			fields = append(fields, f.Name)
			continue
		}

		// Nested object — include scalar fields if depth allows (max 15 nested fields to keep queries reasonable)
		if depth > 0 {
			if nestedDef, ok := p.doc.Types[innerType]; ok && nestedDef.Kind == ast.Object {
				nestedFields := p.selectScalarFields(nestedDef, 0)
				if nestedFields != "" {
					// Limit nested selections to keep query size reasonable
					parts := strings.Fields(nestedFields)
					if len(parts) > 8 {
						parts = parts[:8]
					}
					fields = append(fields, f.Name+" { "+strings.Join(parts, " ")+" }")
				}
			}
		}
	}
	return strings.Join(fields, " ")
}

func (p *parser) isSingularGet(f *ast.FieldDefinition) bool {
	// A singular get typically has exactly one required ID argument
	if len(f.Arguments) == 0 {
		return false
	}
	retType := resolveReturnTypeName(f.Type)
	_, isPaginator := p.paginators[retType]
	return !isPaginator && len(f.Arguments) <= 3
}

// matchMutationToResource tries to find the best resource group for a mutation.
func (p *parser) matchMutationToResource(mutationName string, resources map[string]*Resource) string {
	// Common patterns: createJob, updateJob, deleteJob, terminateHire, acceptBid
	patterns := []struct {
		prefix string
		strip  bool
	}{
		{"create", true},
		{"update", true},
		{"delete", true},
		{"remove", true},
		{"cancel", true},
		{"terminate", true},
		{"approve", true},
		{"reject", true},
		{"share", true},
		{"end", true},
		{"open", true},
		{"duplicate", true},
		{"attach", true},
		{"detach", true},
		{"invite", true},
		{"run", true},
		{"generate", true},
		{"manage", true},
		{"mark", true},
		{"upload", true},
		{"set", true},
		{"store", true},
		{"send", true},
		{"change", true},
		{"verify", true},
		{"onboard", true},
		{"action", true},
		{"retry", true},
		{"attribute", true},
	}

	lower := mutationName
	for _, pat := range patterns {
		if strings.HasPrefix(strings.ToLower(lower), pat.prefix) {
			rest := lower[len(pat.prefix):]
			if len(rest) > 0 && unicode.IsUpper(rune(rest[0])) {
				candidate := toKebabCase(rest)
				// Check if this maps to an existing resource
				if _, ok := resources[candidate]; ok {
					return candidate
				}
				// Try plural form
				plural := toKebabCase(rest) + "s"
				if _, ok := resources[plural]; ok {
					return plural
				}
				// Try singular of the candidate
				singular := toKebabCase(toSingular(rest))
				if _, ok := resources[singular]; ok {
					return singular
				}
				// Use the candidate as-is (will create new resource)
				return candidate
			}
		}
	}

	// Fallback: use the full mutation name as resource
	return toKebabCase(mutationName)
}

// deriveMutationCLIName extracts the CLI action name from a mutation name relative to its resource.
func (p *parser) deriveMutationCLIName(mutationName, resourceName string) string {
	resourcePascal := toPascalCase(resourceName)

	// Standard CRUD patterns
	crudPrefixes := map[string]string{
		"create": "create",
		"update": "update",
		"delete": "delete",
	}

	for prefix, cliName := range crudPrefixes {
		if strings.HasPrefix(mutationName, prefix) {
			return cliName
		}
	}

	// Try to strip the resource name to get the verb
	// e.g., "terminateHire" -> "terminate", "acceptBid" -> "accept"
	lower := strings.ToLower(mutationName)
	resLower := strings.ToLower(resourcePascal)

	if idx := strings.Index(lower, resLower); idx > 0 {
		verb := mutationName[:idx]
		return toKebabCase(verb)
	}

	// Fallback: use the full mutation name in kebab-case
	return toKebabCase(mutationName)
}

// Helper functions

func unwrapType(t *ast.Type) string {
	if t.Elem != nil {
		return unwrapType(t.Elem)
	}
	return t.NamedType
}

func resolveReturnTypeName(t *ast.Type) string {
	if t.Elem != nil {
		return resolveReturnTypeName(t.Elem)
	}
	return t.NamedType
}

func isBuiltinType(name string) bool {
	return strings.HasPrefix(name, "__") ||
		name == "_Any" ||
		name == "_Entity" ||
		name == "_Service" ||
		name == "_FieldSet"
}

func cleanDescription(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "  ", " ")
	return s
}

func hasDeprecated(dirs ast.DirectiveList) bool {
	return getDirective(dirs, "deprecated") != nil
}

func getDirective(dirs ast.DirectiveList, name string) *ast.Directive {
	for _, d := range dirs {
		if d.Name == name {
			return d
		}
	}
	return nil
}

func getDirectiveArg(d *ast.Directive, name string) string {
	for _, a := range d.Arguments {
		if a.Name == name {
			return a.Value.Raw
		}
	}
	return ""
}

func defaultValueString(v *ast.Value) string {
	if v == nil {
		return ""
	}
	return v.Raw
}

func ptrType(goType string) string {
	if strings.HasPrefix(goType, "map[") || strings.HasPrefix(goType, "[]") {
		return goType // maps and slices are already reference types
	}
	return "*" + goType
}

// toPascalCase converts a string to PascalCase.
// "helloWorld" -> "HelloWorld", "hello-world" -> "HelloWorld", "hello_world" -> "HelloWorld"
func toPascalCase(s string) string {
	if s == "" {
		return s
	}

	var result strings.Builder
	capitalizeNext := true

	for _, r := range s {
		if r == '-' || r == '_' {
			capitalizeNext = true
			continue
		}
		if capitalizeNext {
			result.WriteRune(unicode.ToUpper(r))
			capitalizeNext = false
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

var camelToKebab = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// toKebabCase converts a string to kebab-case.
// "helloWorld" -> "hello-world", "HTMLParser" -> "html-parser"
func toKebabCase(s string) string {
	s = camelToKebab.ReplaceAllString(s, "${1}-${2}")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

// toSingular is a simple heuristic to singularize a word.
func toSingular(s string) string {
	if strings.HasSuffix(s, "ies") {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "sses") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "ses") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && !strings.HasSuffix(s, "us") {
		return s[:len(s)-1]
	}
	return s
}
