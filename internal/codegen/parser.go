package codegen

import (
	"bytes"
	"encoding/json"
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
	Resources       map[string]OverrideResource `yaml:"resources"`
	Ignore          []string                    `yaml:"ignore"`
	IgnoreMutations []string                    `yaml:"ignore_mutations"`
	// Aliases maps a resource name to extra names it also answers to, so a
	// rename in the API schema doesn't break the old invocation.
	Aliases map[string][]string `yaml:"aliases"`
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
	"DecimalFour":      "float64",
	"Percentage":       "float64",
	"StrictPercentage": "float64",
	"Upload":           "string",
	"Json":             "map[string]any",
	"JSON":             "map[string]any",
	"Dictionary":       "map[string]any",
	"URL":              "string",
	"E164PhoneNumber":  "string",
	"Email":            "string",
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
		doc:        doc,
		overrides:  overrides,
		enums:      make(map[string]bool),
		inputs:     make(map[string]bool),
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

	aliasErrors []string
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
	var unmapped []string
	for name, def := range p.doc.Types {
		if def.Kind == ast.Scalar && !isBuiltinType(name) {
			schema.Scalars = append(schema.Scalars, name)
			if _, ok := scalarMap[name]; !ok {
				unmapped = append(unmapped, name)
			}
		}
	}
	sort.Strings(schema.Scalars)

	// An unmapped scalar otherwise generates code referencing a Go type that
	// doesn't exist, surfacing as "undefined: DecimalFour" from the compiler
	// long after the point of failure. Say so here instead.
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		return nil, fmt.Errorf(
			"schema declares scalar(s) with no Go mapping: %s\nadd them to scalarMap in internal/codegen/parser.go",
			strings.Join(unmapped, ", "))
	}

	// Parse operations and group into resources
	schema.Resources = p.buildResources()

	if len(p.aliasErrors) > 0 {
		return nil, fmt.Errorf("invalid aliases in overrides:\n  %s",
			strings.Join(p.aliasErrors, "\n  "))
	}

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
		Name:         f.Name,
		GoName:       toPascalCase(f.Name),
		CLIFlag:      toKebabCase(f.Name),
		Description:  cleanDescription(f.Description),
		Type:         p.resolveType(f.Type),
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
	obj.Implements = append(obj.Implements, def.Interfaces...)
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
	u.Members = append(u.Members, def.Types...)
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
		// For optional lists, nil slice is fine — no pointer wrapper needed.
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
		if def := p.doc.Types[t.NamedType]; def != nil {
			for _, v := range def.EnumValues {
				ref.EnumValues = append(ref.EnumValues, v.Name)
			}
		}
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
	ignoredMutations := make(map[string]bool)
	for _, name := range p.overrides.IgnoreMutations {
		ignoredMutations[name] = true
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
			if !ignored[f.Name] && !ignoredMutations[f.Name] {
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
				op.CLIName = p.deriveMutationCLIName(mName, name)
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

	// Match plural queries to resource names (sorted for deterministic output)
	pluralNames := make([]string, 0, len(pluralQueries))
	for name := range pluralQueries {
		pluralNames = append(pluralNames, name)
	}
	sort.Strings(pluralNames)
	for _, pluralName := range pluralNames {
		pluralField := pluralQueries[pluralName]
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

	// Handle remaining singular queries that don't have a plural counterpart (sorted)
	singularNames := make([]string, 0, len(singularQueries))
	for name := range singularQueries {
		if !claimed[name] {
			singularNames = append(singularNames, name)
		}
	}
	sort.Strings(singularNames)
	for _, name := range singularNames {
		f := singularQueries[name]
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

	// Handle remaining unclaimed queries (sorted)
	remainingQueryNames := make([]string, 0)
	for name := range queries {
		if !claimed[name] {
			remainingQueryNames = append(remainingQueryNames, name)
		}
	}
	sort.Strings(remainingQueryNames)
	for _, name := range remainingQueryNames {
		f := queries[name]
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

	// Match mutations to resources (sorted for deterministic output)
	mutationNames := make([]string, 0, len(mutations))
	for name := range mutations {
		if !claimed[name] {
			mutationNames = append(mutationNames, name)
		}
	}
	sort.Strings(mutationNames)
	for _, name := range mutationNames {
		f := mutations[name]
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

	// Post-processing: merge singular/plural resource pairs (sorted for deterministic output)
	mergeNames := make([]string, 0, len(resourceMap))
	for name := range resourceMap {
		mergeNames = append(mergeNames, name)
	}
	sort.Strings(mergeNames)
	for _, name := range mergeNames {
		res := resourceMap[name]
		if res.GetQuery != nil && res.ListQuery == nil {
			plural := toPlural(name)
			if pluralRes, ok := resourceMap[plural]; ok && pluralRes.ListQuery != nil && pluralRes.GetQuery == nil {
				// Merge: move GetQuery and Mutations from singular into plural
				pluralRes.GetQuery = res.GetQuery
				pluralRes.Mutations = append(pluralRes.Mutations, res.Mutations...)
				if pluralRes.Description == "" {
					pluralRes.Description = res.Description
				}
				delete(resourceMap, name)
			}
		}
	}

	// Post-processing: fill empty descriptions and detect hoisted resources
	for _, res := range resourceMap {
		// Fill empty descriptions
		if res.Description == "" {
			if res.GetQuery != nil && res.GetQuery.Description != "" {
				res.Description = res.GetQuery.Description
			} else if res.ListQuery != nil && res.ListQuery.Description != "" {
				res.Description = res.ListQuery.Description
			} else if len(res.Mutations) == 1 && res.Mutations[0].Description != "" {
				res.Description = res.Mutations[0].Description
			} else if len(res.Mutations) > 1 {
				// Multiple mutations: use generic "Manage <resource>." instead of first mutation's description
				displayName := res.Name
				if !strings.HasSuffix(displayName, "s") || strings.HasSuffix(displayName, "ss") || strings.HasSuffix(displayName, "us") {
					displayName = toPlural(displayName)
				}
				res.Description = fmt.Sprintf("Manage %s.", strings.ReplaceAll(displayName, "-", " "))
			}
		}

		// Issue #1: detect hoisted single-mutation resources
		if res.GetQuery == nil && res.ListQuery == nil && len(res.Mutations) == 1 && res.Mutations[0].CLIName == res.Name {
			res.Hoisted = true
		}
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

		// Generate table columns from the return type
		res.TableColumns = p.buildTableColumns(res)
		res.Aliases = p.overrides.Aliases[res.Name]

		resources = append(resources, *res)
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Name < resources[j].Name
	})

	// An alias naming a resource that no longer exists is silently dead, and
	// an alias colliding with a real resource shadows it. Both mean the
	// overrides file is stale.
	byName := make(map[string]bool, len(resources))
	for _, r := range resources {
		byName[r.Name] = true
	}
	for target, aliases := range p.overrides.Aliases {
		if !byName[target] {
			p.aliasErrors = append(p.aliasErrors,
				fmt.Sprintf("alias target %q is not a generated resource", target))
			continue
		}
		for _, a := range aliases {
			if byName[a] {
				p.aliasErrors = append(p.aliasErrors,
					fmt.Sprintf("alias %q (for %q) collides with a real resource", a, target))
			}
		}
	}
	sort.Strings(p.aliasErrors)

	return resources
}

// buildTableColumns generates table column definitions for a resource.
// It inspects the return type of the get query (or the paginated type from
// the list query) and produces columns for scalar fields and up to 2 fields
// from nested objects. Max 8 columns total, with "id" first if present.
func (p *parser) buildTableColumns(res *Resource) []TableColumn {
	// Determine which type to inspect: prefer get query return type,
	// fall back to list query's paginated type.
	var typeName string
	if res.GetQuery != nil {
		typeName = res.GetQuery.ReturnType.Name
	} else if res.ListQuery != nil {
		ret := res.ListQuery.ReturnType
		if ret.IsPaginator && ret.PaginatedType != "" {
			typeName = ret.PaginatedType
		} else {
			typeName = ret.Name
		}
	}
	return p.buildTableColumnsForType(typeName)
}

// buildTableColumnsForType generates table column definitions for a given GraphQL type name.
// It produces columns for scalar fields and up to 2 fields from nested objects.
// Max 8 columns total, with "id" first if present.
func (p *parser) buildTableColumnsForType(typeName string) []TableColumn {
	if typeName == "" {
		return nil
	}

	// Check if the original type is a multi-member union; if so we will
	// prepend a "Type" column after resolving columns from the concrete type.
	isMultiMemberUnion := false
	originalDef := p.doc.Types[typeName]
	if originalDef != nil && originalDef.Kind == ast.Union && len(originalDef.Types) > 1 {
		isMultiMemberUnion = true
	}

	// Resolve union types: use a shared interface if all members implement one,
	// otherwise fall back to the first union member.
	def := p.resolveUnionForColumns(typeName)
	if def == nil {
		return nil
	}

	const maxColumns = 8

	var columns []TableColumn
	var idCol *TableColumn

	for _, f := range def.Fields {
		if len(columns) >= maxColumns {
			break
		}
		if f.Name == "__typename" {
			continue
		}
		if hasRequiredArgs(f) {
			continue
		}

		innerType := unwrapType(f.Type)

		// Scalar or enum field
		if knownScalars[innerType] || p.enums[innerType] {
			col := TableColumn{
				Header: toTitleCase(f.Name),
				Field:  f.Name,
			}
			if f.Name == "id" {
				idCol = &col
			} else {
				columns = append(columns, col)
			}
			continue
		}

		// Nested object/interface: include up to 2 scalar fields as "parent.child"
		nestedDef := p.doc.Types[innerType]
		if nestedDef == nil || (nestedDef.Kind != ast.Object && nestedDef.Kind != ast.Interface) {
			continue
		}
		nestedCount := 0
		for _, nf := range nestedDef.Fields {
			if nestedCount >= 2 || len(columns) >= maxColumns {
				break
			}
			if !p.nestedFieldSelected(nf) {
				continue
			}
			columns = append(columns, TableColumn{
				Header: toTitleCase(f.Name) + " " + toTitleCase(nf.Name),
				Field:  f.Name + "." + nf.Name,
			})
			nestedCount++
		}
	}

	// Put "id" first if present
	if idCol != nil {
		columns = append([]TableColumn{*idCol}, columns...)
	}

	// For multi-member unions, prepend a "Type" column so the user can see
	// which concrete type each row is.
	if isMultiMemberUnion {
		columns = append([]TableColumn{{Header: "Type", Field: "__typename"}}, columns...)
	}

	// Enforce max columns
	if len(columns) > maxColumns {
		columns = columns[:maxColumns]
	}

	return columns
}

// resolveUnionForColumns resolves a type name to an ast.Definition suitable for
// extracting table columns. If the type is already an object or interface, it is returned
// directly. If it is a union, the function looks for a shared interface that all
// members implement; failing that it falls back to the first union member.
func (p *parser) resolveUnionForColumns(typeName string) *ast.Definition {
	def := p.doc.Types[typeName]
	if def == nil {
		return nil
	}
	if def.Kind == ast.Object || def.Kind == ast.Interface {
		return def
	}
	if def.Kind != ast.Union || len(def.Types) == 0 {
		return nil
	}

	// Try to find a shared interface implemented by all union members.
	if iface := p.findSharedInterface(def); iface != nil {
		return iface
	}

	// Fallback: use the first union member.
	firstMember := def.Types[0]
	return p.doc.Types[firstMember]
}

// findSharedInterface returns an interface definition that all members of the
// union implement, preferring the interface with the most fields. Returns nil
// if no common interface exists.
func (p *parser) findSharedInterface(unionDef *ast.Definition) *ast.Definition {
	if len(unionDef.Types) == 0 {
		return nil
	}

	// Collect interfaces implemented by the first member.
	firstDef := p.doc.Types[unionDef.Types[0]]
	if firstDef == nil {
		return nil
	}
	candidates := make(map[string]bool, len(firstDef.Interfaces))
	for _, iface := range firstDef.Interfaces {
		candidates[iface] = true
	}

	// Intersect with interfaces of all other members.
	for _, memberName := range unionDef.Types[1:] {
		memberDef := p.doc.Types[memberName]
		if memberDef == nil {
			return nil
		}
		memberIfaces := make(map[string]bool, len(memberDef.Interfaces))
		for _, iface := range memberDef.Interfaces {
			memberIfaces[iface] = true
		}
		for iface := range candidates {
			if !memberIfaces[iface] {
				delete(candidates, iface)
			}
		}
	}

	// Pick the shared interface with the most fields.
	var best *ast.Definition
	for ifaceName := range candidates {
		ifaceDef := p.doc.Types[ifaceName]
		if ifaceDef == nil {
			continue
		}
		if best == nil || len(ifaceDef.Fields) > len(best.Fields) {
			best = ifaceDef
		}
	}
	return best
}

// toTitleCase converts a camelCase or snake_case field name to a display title.
// "id" -> "ID", "firstName" -> "First Name", "company_name" -> "Company Name"
func toTitleCase(s string) string {
	if strings.EqualFold(s, "id") {
		return "ID"
	}

	// Split on camelCase boundaries and underscores/hyphens
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		if unicode.IsUpper(rune(s[i])) || s[i] == '_' || s[i] == '-' {
			word := s[start:i]
			if word != "" && word != "_" && word != "-" {
				words = append(words, word)
			}
			start = i
			if s[i] == '_' || s[i] == '-' {
				start = i + 1
			}
		}
	}
	if start < len(s) {
		words = append(words, s[start:])
	}

	// Title-case each word
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}

	return strings.Join(words, " ")
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
		// Build table columns from the mutation's return type
		op.TableColumns = p.buildTableColumnsForType(op.ReturnType.Name)
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
	op.InputFieldCount = len(inputDef.Fields)

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

	// Build a JSON example showing the full input structure (including nested types)
	op.InputExample = p.buildInputExample(arg.Type.Name)
}

// buildInputExample recursively builds a JSON example string for a GraphQL input type.
// It produces a pretty-printed JSON object with placeholder values for each field.
func (p *parser) buildInputExample(inputTypeName string) string {
	example := p.buildInputExampleValue(inputTypeName, 0)
	if example == nil {
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(example); err != nil {
		return ""
	}
	// Encode adds a trailing newline; trim it
	return strings.TrimSpace(buf.String())
}

// buildInputExampleValue recursively builds a map/slice/value representing an
// example JSON payload for the given input type. depth controls recursion to
// avoid infinite loops (max depth 2).
func (p *parser) buildInputExampleValue(inputTypeName string, depth int) map[string]any {
	if depth > 2 {
		return nil
	}

	def := p.doc.Types[inputTypeName]
	if def == nil || def.Kind != ast.InputObject {
		return nil
	}

	result := make(map[string]any)
	for _, f := range def.Fields {
		result[f.Name] = p.exampleValueForType(f.Type, depth)
	}
	return result
}

// exampleValueForType returns an example value for a GraphQL type, suitable for
// JSON serialization. It handles scalars, enums, lists, and nested input objects.
func (p *parser) exampleValueForType(t *ast.Type, depth int) any {
	// List type: wrap inner value in a slice
	if t.Elem != nil {
		inner := p.exampleValueForType(t.Elem, depth)
		return []any{inner}
	}

	typeName := t.NamedType

	// Scalar types
	switch typeName {
	case "String":
		return "..."
	case "Int":
		return 0
	case "Float", "Decimal", "DecimalTwo", "DecimalFour", "Percentage", "StrictPercentage":
		return 0.0
	case "Boolean":
		return false
	case "ID":
		return "<id>"
	case "DateTime":
		return "2024-01-01T00:00:00Z"
	case "Date":
		return "2024-01-01"
	case "Time":
		return "12:00:00"
	case "Upload":
		return "<file>"
	case "URL":
		return "https://example.com"
	case "E164PhoneNumber":
		return "+1234567890"
	case "Json", "JSON", "Dictionary":
		return map[string]any{}
	}

	// Other known scalars not covered above
	if knownScalars[typeName] {
		return "..."
	}

	// Enum: show first enum value (or first few)
	if enumDef, ok := p.doc.Types[typeName]; ok && enumDef.Kind == ast.Enum {
		if len(enumDef.EnumValues) > 0 {
			return enumDef.EnumValues[0].Name
		}
		return "..."
	}

	// Nested input object
	if p.inputs[typeName] {
		nested := p.buildInputExampleValue(typeName, depth+1)
		if nested != nil {
			return nested
		}
		return map[string]any{}
	}

	return "..."
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

		// If the paginated type is a union, build inline fragment selections.
		if innerDef.Kind == ast.Union {
			unionFields := p.buildUnionSelectionFields(innerDef, 1)
			if unionFields == "" {
				return "{ paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { __typename id } }"
			}
			return "{ paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { __typename " + unionFields + " } }"
		}

		dataFields := p.selectScalarFields(innerDef, 1)
		return "{ paginatorInfo { count currentPage hasMorePages lastPage perPage total } data { " + typenamePrefix(innerDef) + dataFields + " } }"
	}

	// Check if it's a union type
	def := p.doc.Types[typeName]
	if def != nil && def.Kind == ast.Union {
		unionFields := p.buildUnionSelectionFields(def, depth)
		if unionFields == "" {
			return "{ __typename id }"
		}
		return "{ __typename " + unionFields + " }"
	}

	// For known object and interface types, select scalar fields. Interfaces
	// need a selection set just like objects — the server rejects a bare field.
	if def == nil || (def.Kind != ast.Object && def.Kind != ast.Interface) {
		return ""
	}
	fields := p.selectScalarFields(def, depth)
	if fields == "" {
		return "{ id }"
	}
	return "{ " + typenamePrefix(def) + fields + " }"
}

// typenamePrefix returns "__typename " for interface types. An interface
// selection is the fields every implementor shares, so without it a list of
// accounts gives no way to tell a Worker from a Company. Unions already do
// this; interfaces are the same problem.
func typenamePrefix(def *ast.Definition) string {
	if def != nil && def.Kind == ast.Interface {
		return "__typename "
	}
	return ""
}

// buildUnionSelectionFields generates the inline fragment portion of a selection
// set for a union type. If all members share a common interface, it uses a single
// "... on InterfaceName { fields }" fragment. Otherwise it generates separate
// inline fragments for each member type.
func (p *parser) buildUnionSelectionFields(unionDef *ast.Definition, depth int) string {
	// Try a shared interface first for a compact selection.
	if iface := p.findSharedInterface(unionDef); iface != nil {
		fields := p.selectScalarFields(iface, depth)
		if fields != "" {
			return "... on " + iface.Name + " { " + fields + " }"
		}
	}

	// Fallback: inline fragment per member.
	var parts []string
	for _, memberName := range unionDef.Types {
		memberDef := p.doc.Types[memberName]
		if memberDef == nil {
			continue
		}
		fields := p.selectScalarFields(memberDef, depth)
		if fields != "" {
			parts = append(parts, "... on "+memberName+" { "+fields+" }")
		}
	}
	return strings.Join(parts, " ")
}

// safeNestedFields defines the set of field names that are safe to request on
// nested objects. Many GraphQL APIs restrict access to sensitive fields (e.g.
// canCreatePassword, missingAuthentication) when the viewer isn't the resource
// owner. By limiting nested selections to common identifying/display fields, we
// avoid triggering access-control errors.
//
// Keyed on bare field names, so an entry matches that name on every type:
// "state" admits an approval's workflow state and an address's geographic one
// alike. Type-scoped keys ("Type.field") would match on meaning rather than
// spelling; not needed while every entry here is safe on all its homonyms.
var safeNestedFields = map[string]bool{
	"id": true, "name": true, "email": true, "avatar": true,
	"status": true, "type": true, "currency": true, "market": true,
	"number": true, "description": true, "firstName": true, "lastName": true,
	"middleName": true, "phone": true, "initials": true, "subject": true,
	"url": true, "title": true, "label": true, "slug": true, "code": true,
	"createdAt": true, "updatedAt": true, "startDate": true, "endDate": true,
	// State fields. Without these a nested row is unreadable — a hire's
	// compliances came back as catalogue entries with no indication of which
	// applied or were already done, and an approval state carried no state.
	//
	// Named individually rather than admitting every enum: introspection strips
	// @passes/@guard, so the generator cannot see which fields are access
	// controlled. Selecting by type would be default-allow on a property the
	// schema we parse does not carry.
	"actor": true, "state": true, "cancellationReason": true,
	"applicable": true, "completed": true, "completedAt": true,
}

// selectScalarFields returns a space-separated list of scalar field selections for a type.
// At depth > 0 (top level), it includes all scalar/enum fields and recurses into nested
// objects with safe-only field selection. At depth 0, all scalar/enum fields are still
// included — this is used only for the primary return type in union/paginator contexts.
func (p *parser) selectScalarFields(def *ast.Definition, depth int) string {
	var fields []string
	for _, f := range def.Fields {
		if f.Name == "__typename" {
			continue
		}
		// Skip fields the auto-generated selection set cannot call correctly.
		if p.needsArgument(f) {
			continue
		}
		innerType := unwrapType(f.Type)

		// Scalar or enum field — always include
		if knownScalars[innerType] || p.enums[innerType] {
			fields = append(fields, f.Name)
			continue
		}

		// Nested object — include only safe identifying fields to avoid access-control errors
		if depth > 0 {
			if nestedDef, ok := p.doc.Types[innerType]; ok && (nestedDef.Kind == ast.Object || nestedDef.Kind == ast.Interface) {
				nestedFields := p.selectSafeFields(nestedDef)
				if nestedFields != "" {
					fields = append(fields, f.Name+" { "+typenamePrefix(nestedDef)+nestedFields+" }")
				}
			}
		}
	}
	return strings.Join(fields, " ")
}

// selectSafeFields returns only safe identifying/display fields for a nested object.
// This avoids requesting access-restricted fields on types like User, Worker, etc.
func (p *parser) selectSafeFields(def *ast.Definition) string {
	var fields []string
	for _, f := range def.Fields {
		if p.nestedFieldSelected(f) {
			fields = append(fields, f.Name)
		}
	}
	return strings.Join(fields, " ")
}

// nestedFieldSelected reports whether a nested selection will include f.
// buildTableColumnsForType asks the same question, so both must ask it here:
// a column for a field the query never selects renders blank, and a field the
// query does fetch deserves its column. Widening what nested selections
// include must widen the columns with it.
func (p *parser) nestedFieldSelected(f *ast.FieldDefinition) bool {
	if f.Name == "__typename" || p.needsArgument(f) {
		return false
	}
	innerType := unwrapType(f.Type)
	return (knownScalars[innerType] || p.enums[innerType]) && safeNestedFields[f.Name]
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
				// Try suffix matching: split the remaining PascalCase words and
				// check if any suffix (last word, last two words, ...) matches
				// an existing resource. This handles multi-word mutations like
				// "attributeRecruiterToHire" where "Hire"/"hires" is a resource.
				if match := matchSuffix(rest, resources); match != "" {
					return match
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

	// Standard CRUD patterns: only use the short form ("create") if the rest
	// matches the resource name exactly. Otherwise keep the full suffix to
	// disambiguate (e.g., "createSmsMultiFactor" under "multi-factors" → "create-sms").
	crudPrefixes := []string{"create", "update", "delete"}

	for _, prefix := range crudPrefixes {
		if strings.HasPrefix(mutationName, prefix) {
			rest := mutationName[len(prefix):]
			restLower := strings.ToLower(rest)
			resLower := strings.ToLower(resourcePascal)
			resSingular := strings.ToLower(toSingular(resourcePascal))
			if restLower == resLower || restLower == resSingular {
				return prefix
			}
			// The rest doesn't match the resource — include the distinguishing part.
			// e.g., "createCustomTimesheet" under "timesheets" → "create-custom"
			// Strip the resource suffix from rest to get the qualifier.
			words := splitPascalWords(rest)
			resWords := splitPascalWords(resourcePascal)
			resSingWords := splitPascalWords(toPascalCase(toSingular(resourceName)))
			qualifier := stripResourceSuffix(words, resWords, resSingWords)
			if qualifier != "" {
				return prefix + "-" + toKebabCase(qualifier)
			}
			return toKebabCase(mutationName)
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

	// Also try the singular form of the resource name so that e.g.
	// "terminateHire" under "hires" matches "hire" and yields "terminate".
	resSingularLower := strings.ToLower(toPascalCase(toSingular(resourceName)))
	if idx := strings.Index(lower, resSingularLower); idx > 0 {
		verb := mutationName[:idx]
		return toKebabCase(verb)
	}

	// Fallback: use the full mutation name in kebab-case
	return toKebabCase(mutationName)
}

// stripResourceSuffix removes the resource name words from the end of a word list
// and returns the remaining words joined. For example:
// words=["Custom","Timesheet"], resWords=["Timesheets"] → "Custom"
// words=["Sms","Multi","Factor"], resWords=["Multi","Factors"] → "Sms"
func stripResourceSuffix(words, resWords, resSingWords []string) string {
	// Try matching singular resource words from the end
	for _, rw := range [][]string{resSingWords, resWords} {
		if len(rw) > 0 && len(rw) < len(words) {
			match := true
			for i := range rw {
				wi := len(words) - len(rw) + i
				if !strings.EqualFold(words[wi], rw[i]) {
					match = false
					break
				}
			}
			if match {
				remaining := words[:len(words)-len(rw)]
				return strings.Join(remaining, "")
			}
		}
	}
	return ""
}

// Helper functions

// needsArgument reports whether a field cannot be selected without an argument
// the generated query does not supply. That is any required argument — and
// also a nullable, non-list enum or input-object argument with no default.
// Such an argument is a choice the resolver expects the caller to make:
// `triggersApproval(trigger: ApprovalTrigger): Boolean` is valid GraphQL
// without it, but the resolver has nothing to evaluate and fails at runtime.
// Optional list arguments ("names to filter by; omit for all") and arguments
// with defaults are genuinely optional and stay selectable.
func (p *parser) needsArgument(f *ast.FieldDefinition) bool {
	if hasRequiredArgs(f) {
		return true
	}
	for _, arg := range f.Arguments {
		if arg.DefaultValue != nil || arg.Type.Elem != nil {
			continue
		}
		name := arg.Type.NamedType
		if p.enums[name] || p.inputs[name] {
			return true
		}
	}
	return false
}

// hasRequiredArgs returns true if a field has any required (non-null) arguments.
func hasRequiredArgs(f *ast.FieldDefinition) bool {
	for _, arg := range f.Arguments {
		if arg.Type.NonNull && arg.DefaultValue == nil {
			return true
		}
	}
	return false
}

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
	// pflag reads a back-quoted word in a flag's usage string as the value
	// placeholder: "set by the `accounts` arg" renders the flag as
	// `--viewer-role accounts` and strips the quotes. Schema descriptions use
	// backticks as code spans, so swap them for plain quotes before they can
	// reach a usage string.
	s = strings.ReplaceAll(s, "`", "'")
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

// splitPascalWords splits a PascalCase string into individual words at uppercase boundaries.
// "RecruiterToHire" -> ["Recruiter", "To", "Hire"]
// "Job" -> ["Job"]
// "DraftHire" -> ["Draft", "Hire"]
func splitPascalWords(s string) []string {
	if s == "" {
		return nil
	}
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		if unicode.IsUpper(rune(s[i])) {
			words = append(words, s[start:i])
			start = i
		}
	}
	words = append(words, s[start:])
	return words
}

// matchSuffix tries to match suffix subsets of a PascalCase string against known resources.
// It checks from the shortest suffix (last word) to longer suffixes, returning the first match.
// For example, given "RecruiterToHire" and a resource "hires", it will try:
//   - "Hire" -> kebab "hire" -> not found -> plural "hires" -> found -> return "hires"
func matchSuffix(rest string, resources map[string]*Resource) string {
	words := splitPascalWords(rest)
	if len(words) <= 1 {
		// Already tried the full string; nothing to suffix-match.
		return ""
	}
	// Try suffixes from shortest (last word) to longest (all but first word).
	for i := len(words) - 1; i >= 1; i-- {
		suffix := strings.Join(words[i:], "")
		candidate := toKebabCase(suffix)
		if _, ok := resources[candidate]; ok {
			return candidate
		}
		plural := candidate + "s"
		if _, ok := resources[plural]; ok {
			return plural
		}
		singular := toKebabCase(toSingular(suffix))
		if _, ok := resources[singular]; ok {
			return singular
		}
	}
	return ""
}

// toPlural is a simple heuristic to pluralize a word.
func toPlural(s string) string {
	if strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh") || strings.HasSuffix(s, "ss") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") {
		return s + "es"
	}
	if strings.HasSuffix(s, "y") && len(s) > 1 && !isVowel(s[len(s)-2]) {
		return s[:len(s)-1] + "ies"
	}
	return s + "s"
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	}
	return false
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
