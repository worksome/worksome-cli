// Command generate parses the vendored GraphQL schema and generates Go code
// for types, queries, and CLI commands.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/worksome/worksome-cli/internal/codegen"
)

func main() {
	schemaPath := flag.String("schema", "schema/schema.graphql", "Path to GraphQL schema file")
	overridesPath := flag.String("overrides", "schema/overrides.yaml", "Path to overrides YAML file")
	outputDir := flag.String("output", "internal/generated", "Output directory for generated code")
	modulePath := flag.String("module", "github.com/worksome/worksome-cli", "Go module path")
	readmePath := flag.String("readme", "README.md", "README whose marked counts are rewritten from the schema (\"\" to skip)")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "Parsing schema: %s\n", *schemaPath)
	schema, err := codegen.ParseSchema(*schemaPath, *overridesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing schema: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Parsed: %d resources, %d enums, %d objects, %d inputs\n",
		len(schema.Resources), len(schema.Enums), len(schema.Objects), len(schema.Inputs))

	fmt.Fprintf(os.Stderr, "Generating code to: %s\n", *outputDir)
	gen := codegen.NewGenerator(schema, *outputDir, *modulePath)
	if err := gen.Generate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating code: %v\n", err)
		os.Exit(1)
	}

	// The README quotes how much of the API the CLI covers. Those numbers are
	// rewritten here so they are as generated as the code they describe.
	if *readmePath != "" {
		if _, statErr := os.Stat(*readmePath); statErr == nil {
			changed, err := codegen.UpdateReadmeCounts(*readmePath, schema)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error updating README counts: %v\n", err)
				os.Exit(1)
			}
			if changed {
				fmt.Fprintf(os.Stderr, "Updated counts in %s\n", *readmePath)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Done!\n")
}
