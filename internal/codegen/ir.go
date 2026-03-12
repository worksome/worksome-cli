// Package codegen provides the schema parser and code generator for the worksome CLI.
// It parses a GraphQL schema into an intermediate representation (IR) and then
// generates Go code from templates.
package codegen

// Schema is the top-level intermediate representation of the parsed GraphQL schema.
type Schema struct {
	Resources  []Resource
	Enums      []Enum
	Objects    []Object
	Inputs     []InputObject
	Interfaces []Interface
	Unions     []Union
	Scalars    []string
}

// Resource represents a CLI resource group (e.g., "hires", "jobs") with its
// associated queries and mutations.
type Resource struct {
	Name        string // kebab-case name, e.g., "hires"
	GoName      string // PascalCase, e.g., "Hires"
	Description string
	GetQuery    *Operation // singular get query, e.g., hire(id: ID!)
	ListQuery   *Operation // plural list query, e.g., hires(...)
	Mutations   []Operation
}

// Operation represents a single GraphQL query or mutation.
type Operation struct {
	Name        string // GraphQL name, e.g., "createJob"
	GoName      string // PascalCase, e.g., "CreateJob"
	CLIName     string // kebab-case CLI action, e.g., "create"
	Description string
	Type        OperationType
	Arguments   []Argument
	ReturnType  TypeRef
	Deprecated  bool
	DeprecatedReason string
}

// OperationType distinguishes queries from mutations.
type OperationType string

const (
	OperationQuery    OperationType = "query"
	OperationMutation OperationType = "mutation"
)

// Argument represents a GraphQL field argument or input field.
type Argument struct {
	Name         string // GraphQL name
	GoName       string // PascalCase
	CLIFlag      string // kebab-case flag name
	Description  string
	Type         TypeRef
	DefaultValue string
}

// TypeRef represents a reference to a GraphQL type, with nullability and list info.
type TypeRef struct {
	Name       string // Base type name, e.g., "String", "Hire", "HirePaginator"
	GoType     string // Go type, e.g., "string", "*string", "[]Hire"
	IsRequired bool
	IsList     bool
	ListItem   *TypeRef // For list types, the element type
	IsEnum     bool
	IsInput    bool
	IsScalar   bool
	IsPaginator bool
	PaginatedType string // For paginator types, the inner data type, e.g., "Hire"
}

// Enum represents a GraphQL enum type.
type Enum struct {
	Name        string
	GoName      string
	Description string
	Values      []EnumValue
}

// EnumValue represents a single value in a GraphQL enum.
type EnumValue struct {
	Name        string // GraphQL value, e.g., "ACTIVE"
	GoName      string // Go const name, e.g., "HireStatusActive"
	Description string
	Deprecated  bool
}

// Object represents a GraphQL object type.
type Object struct {
	Name        string
	GoName      string
	Description string
	Fields      []Field
	Implements  []string // Interface names
}

// Field represents a field on a GraphQL object type.
type Field struct {
	Name        string
	GoName      string
	JSONName    string
	Description string
	Type        TypeRef
	Deprecated  bool
}

// InputObject represents a GraphQL input type.
type InputObject struct {
	Name        string
	GoName      string
	Description string
	Fields      []Argument
}

// Interface represents a GraphQL interface type.
type Interface struct {
	Name        string
	GoName      string
	Description string
	Fields      []Field
	Implementors []string
}

// Union represents a GraphQL union type.
type Union struct {
	Name    string
	GoName  string
	Members []string // Type names
}
