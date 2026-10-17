package edit

import (
	"fmt"
	"strings"
)

// The helpers here read and put back TOML values as they are written, so
// a caller (safekeep) can remember a value exactly and restore it later:
// raw text in, the same raw text out. Line ends are given without \r and
// follow the file's own on the way back in (joinLinesLike).

// GetTOMLTopRaw is a top-level (pre-table) key's value as written: a
// string with its quotes, an array over several lines whole, without the
// trailing comment. ok is false when the key, the file, or a parse is
// missing.
func GetTOMLTopRaw(path, key string) (string, bool) {
	doc, err := tomlFileOf(path)
	if err != nil {
		return "", false
	}
	for _, kv := range doc.root.keys {
		if kv.name == key {
			return noCR(kv.text), true
		}
	}
	return "", false
}

// GetTOMLKeyRaw is GetTOMLTopRaw for key of table `name`.
func GetTOMLKeyRaw(path, name, key string) (string, bool) {
	tables, err := tomlTablesOf(path)
	if err != nil {
		return "", false
	}
	table, ok := tomlTableNamed(tables, name)
	if !ok || table.array {
		return "", false
	}
	for _, kv := range table.keys {
		if kv.name == key {
			return noCR(kv.text), true
		}
	}
	return "", false
}

// GetTOMLTableBlock is table `name` as written: its header line and body,
// up to its last line that isn't blank. Child tables ([name.x]) are blocks
// of their own and not part of it; comment lines right above the next
// header are taken as that header's. ok is false when there is no such
// ordinary table.
func GetTOMLTableBlock(path, name string) (string, bool) {
	raw, err := Read(path)
	if err != nil || raw == nil {
		return "", false
	}
	lines := splitLines(string(raw))
	tables, err := parseTOMLTables(lines)
	if err != nil {
		return "", false
	}
	table, ok := tomlTableNamed(tables, name)
	if !ok || table.array {
		return "", false
	}
	end := tomlBlockEnd(lines, table)
	return noCR(strings.Join(lines[table.from:end], "\n")), true
}

// tomlBlockEnd is where table's own lines end: before the blank lines at
// its end and before a run of comments that sits right on the next header.
func tomlBlockEnd(lines []string, table tomlTableSpan) int {
	end := table.to
	if end < len(lines) { // a header follows: the comments on it are its own
		for end > table.from+1 && isTOMLComment(lines[end-1]) {
			end--
		}
	}
	for end > table.from+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	// never cut into a value: a key's value ends where its span says
	for _, kv := range table.keys {
		if kv.to > end {
			end = kv.to
		}
	}
	return end
}

func isTOMLComment(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

// PutTOMLTableBlock puts block, the text GetTOMLTableBlock gave, in place
// of table `name`: over the table's own lines when it is there, at the
// end of the file when not. An empty block removes the table (its child
// tables stay). block must be that one table, header first.
func PutTOMLTableBlock(path, name, block string) error {
	raw, err := Read(path)
	if err != nil {
		return err
	}
	lines := splitLines(string(raw))
	tables, err := parseTOMLTables(lines)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	table, found := tomlTableNamed(tables, name)
	if found && table.array {
		return fmt.Errorf("%s: %q is an array table, expected an ordinary table", path, name)
	}
	var blockLines []string
	if strings.TrimSpace(block) != "" {
		blockLines = splitLines(strings.TrimRight(noCR(block), "\n"))
		doc, err := parseTOMLFile(blockLines)
		if err != nil {
			return fmt.Errorf("table block for %q: %w", name, err)
		}
		if len(doc.root.keys) > 0 || len(doc.tables) != 1 || doc.tables[0].name != name || doc.tables[0].array {
			return fmt.Errorf("table block for %q is not that one table", name)
		}
	}
	var out []string
	switch {
	case !found && blockLines == nil:
		return nil
	case !found:
		out = trimBlank(lines)
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, blockLines...)
		out = append(out, "")
	case blockLines == nil:
		from, to := table.from, tomlBlockEnd(lines, table)
		for from > 0 && strings.TrimSpace(lines[from-1]) == "" {
			from--
		}
		for to < len(lines) && strings.TrimSpace(lines[to]) == "" {
			to++
		}
		out = append(out, lines[:from]...)
		if to < len(lines) && len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines[to:]...)
		if len(trimBlank(append([]string{}, out...))) == 0 {
			out = []string{""}
		}
	default:
		out = append(out, lines[:table.from]...)
		out = append(out, blockLines...)
		out = append(out, lines[tomlBlockEnd(lines, table):]...)
	}
	return writeTOML(path, out)
}

// DelTOMLTableKey removes key from table `name` and leaves the table's
// header even when nothing is left under it, unlike DelTOMLKey: a table
// the user had, empty or not, stays theirs.
func DelTOMLTableKey(path, name, key string) error {
	raw, err := Read(path)
	if err != nil || raw == nil {
		return err
	}
	lines := splitLines(string(raw))
	table, err := parseTOMLTable(lines, name)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if table.from < 0 {
		return nil
	}
	out := append([]string{}, lines[:table.from+1]...)
	at, found := table.from+1, false
	for _, kv := range table.keys {
		if kv.name == key {
			out = append(out, lines[at:kv.from]...)
			at, found = kv.to, true
		}
	}
	if !found {
		return nil
	}
	out = append(out, lines[at:]...)
	return writeTOML(path, out)
}

// noCR is s with the \r of every \r\n taken out.
func noCR(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSuffix(s, "\r")
}

// TOMLLiteral is v spelled as a TOML value, as the setters here write it:
// a string quoted, an Inline table inline, a Raw as it is.
func TOMLLiteral(v any) string { return tomlLiteral(v) }
