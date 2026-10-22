package profile

import (
	"fmt"
	"strings"
)

// delimiter fences a documentation file's frontmatter.
const delimiter = "---"

// description reads the `description` key from a documentation file's
// frontmatter. The agent sees only the description and the path, so a file
// without one is an error.
func description(path, raw string) (string, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != delimiter {
		return "", fmt.Errorf("%s: no frontmatter", path)
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == delimiter {
			return "", fmt.Errorf("%s: frontmatter has no description", path)
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "description" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value == "" {
			return "", fmt.Errorf("%s: description is empty", path)
		}
		return value, nil
	}
	return "", fmt.Errorf("%s: frontmatter is unterminated", path)
}
