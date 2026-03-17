package output

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// camelToKebab matches boundaries between lowercase/digit and uppercase characters
// for converting camelCase to kebab-case.
var camelToKebab = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// toKebabCase converts a camelCase or PascalCase string to kebab-case.
// "helloWorld" -> "hello-world", "HTMLParser" -> "html-parser"
func toKebabCase(s string) string {
	s = camelToKebab.ReplaceAllString(s, "${1}-${2}")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

// ApplyFilterFlag reads the --filter persistent flag from the root command and
// sets the corresponding flags on cmd. The filter string is a comma-separated
// list of key=value pairs where keys are matched to flag names (converted to
// kebab-case). If a flag is not found, the plural form (appending "s") is also
// tried.
//
// Example: --filter "status=ACTIVE,currency=DKK" sets --status ACTIVE --currency DKK
// (or --statuses ACTIVE if --status doesn't exist).
func ApplyFilterFlag(cmd *cobra.Command) error {
	filterStr, _ := cmd.Root().PersistentFlags().GetString("filter")
	if filterStr == "" {
		return nil
	}

	pairs := strings.Split(filterStr, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid filter format %q: use key=value", pair)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return fmt.Errorf("invalid filter format %q: key cannot be empty", pair)
		}

		// Convert key to flag name format (kebab-case)
		flagName := toKebabCase(key)

		flag := cmd.Flags().Lookup(flagName)
		if flag == nil {
			// Try common plural suffixes: "s", "es"
			for _, suffix := range []string{"s", "es"} {
				if f := cmd.Flags().Lookup(flagName + suffix); f != nil {
					flag = f
					break
				}
			}
		}
		if flag == nil {
			// Last resort: find a flag that starts with the key as prefix
			// (e.g., "account" matches "accounts")
			cmd.Flags().VisitAll(func(f *pflag.Flag) {
				if flag == nil && strings.HasPrefix(f.Name, flagName) {
					flag = f
				}
			})
		}
		if flag == nil {
			return fmt.Errorf("unknown filter key %q (no --%s flag)", key, flagName)
		}

		if err := cmd.Flags().Set(flag.Name, value); err != nil {
			return fmt.Errorf("setting filter %s=%s: %w", key, value, err)
		}
	}
	return nil
}
