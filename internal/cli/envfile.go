package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// envFiles are read in order; later files override earlier ones. Variables
// already set in the process environment always win.
var envFiles = []string{".env.development", ".env.local"}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// environment returns base extended with the application's development env
// files found in root.
func environment(root string, base []string) ([]string, error) {
	present := make(map[string]bool, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		present[key] = true
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()

	fromFiles := make(map[string]string)
	var order []string
	for _, name := range envFiles {
		content, err := dir.ReadFile(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		vars, keys, err := parseEnvFile(string(content))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, key := range keys {
			if _, seen := fromFiles[key]; !seen {
				order = append(order, key)
			}
			fromFiles[key] = vars[key]
		}
	}
	env := append([]string(nil), base...)
	for _, key := range order {
		if !present[key] {
			env = append(env, key+"="+fromFiles[key])
		}
	}
	return env, nil
}

// parseEnvFile reads KEY=VALUE lines. Blank lines and lines starting with #
// are ignored, an "export " prefix is accepted and one pair of matching
// quotes around the value is removed. There is no interpolation and no
// inline comment syntax, so values such as URLs are taken literally. Errors
// name the line, never its content, which may be a secret.
func parseEnvFile(content string) (map[string]string, []string, error) {
	vars := make(map[string]string)
	var keys []string
	number := 0
	for line := range strings.Lines(content) {
		number++
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key = strings.TrimSpace(key)
		if !ok || !envKeyPattern.MatchString(key) {
			return nil, nil, fmt.Errorf("line %d: expected KEY=VALUE", number)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, seen := vars[key]; !seen {
			keys = append(keys, key)
		}
		vars[key] = value
	}
	return vars, keys, nil
}
