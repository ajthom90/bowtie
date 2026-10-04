package store

import "strings"

// maxCategories bounds how many of a program's categories are kept.
const maxCategories = 4

// JoinCategories stores a program's categories in its one category column:
// trimmed, without repeats (any case), at most four, joined with "; ". The
// first stays first, so it still reads as the main category.
func JoinCategories(cats []string) string {
	var out []string
	seen := map[string]bool{}
	for _, c := range cats {
		c = strings.TrimSpace(c)
		k := strings.ToLower(c)
		if c == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
		if len(out) == maxCategories {
			break
		}
	}
	return strings.Join(out, "; ")
}

// SplitCategories undoes JoinCategories.
func SplitCategories(s string) []string {
	var out []string
	for _, c := range strings.Split(s, ";") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}
