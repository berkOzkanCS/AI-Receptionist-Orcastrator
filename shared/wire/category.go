package wire

import "strings"

// CategorySep separates the main category from the subcategory in a hit's
// category path, e.g. "logistics.hours". The upstream fires the coarse main
// category first, then the fine "main.sub" path.
const CategorySep = "."

// SplitCategory breaks a category path into its main and sub parts. A coarse
// (main-only) path like "logistics" returns sub == "".
func SplitCategory(path string) (main, sub string) {
	if i := strings.Index(path, CategorySep); i >= 0 {
		return path[:i], path[i+len(CategorySep):]
	}
	return path, ""
}
