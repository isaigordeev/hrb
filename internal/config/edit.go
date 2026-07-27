package config

// hr.toml is hand-written and heavily commented, so hr edits it as text
// rather than round-tripping it through a TOML marshaller — which would
// reflow every table and drop every comment. Only the `group` line of a
// named feed's block is touched; the rest of the file is byte-identical.

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	feedHeader = regexp.MustCompile(`^\s*\[\[\s*feeds\s*\]\]`)
	tableStart = regexp.MustCompile(`^\s*\[`)
	keyLine    = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)
)

// SetGroups rewrites the `group` key of each named feed in the config at
// path. A feed mapped to "" has its group key removed. Feeds named in
// groups but absent from the file are ignored — the vault's directories,
// not hr.toml, are the source of truth for where a feed lives. Returns
// the number of feed blocks changed; the file is left untouched when
// that is zero.
func SetGroups(path string, groups map[string]string) (int, error) {
	if len(groups) == 0 {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read config: %w", err)
	}
	text := string(data)
	// Preserve the file's final-newline state exactly.
	trailingNewline := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")

	out := make([]string, 0, len(lines)+len(groups))
	changed := 0
	for i := 0; i < len(lines); {
		if !feedHeader.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		end := blockEnd(lines, i)
		block, did := applyGroup(lines[i:end], groups)
		if did {
			changed++
		}
		out = append(out, block...)
		i = end
	}
	if changed == 0 {
		return 0, nil
	}

	next := strings.Join(out, "\n")
	if trailingNewline {
		next += "\n"
	}
	if err := writeFileAtomic(path, []byte(next)); err != nil {
		return 0, err
	}
	return changed, nil
}

// blockEnd returns the index just past the [[feeds]] block starting at
// start — the next table header, or EOF.
func blockEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		if tableStart.MatchString(lines[i]) {
			return i
		}
	}
	return len(lines)
}

// applyGroup rewrites one [[feeds]] block in place if its name is in
// groups, reporting whether anything actually changed.
func applyGroup(block []string, groups map[string]string) ([]string, bool) {
	name, nameAt := "", -1
	groupAt := -1
	for i, l := range block {
		switch key(l) {
		case "name":
			if v, ok := stringValue(l); ok {
				name, nameAt = v, i
			}
		case "group":
			groupAt = i
		}
	}
	group, ok := groups[name]
	if name == "" || !ok {
		return block, false
	}

	next := append([]string(nil), block...)
	switch {
	case group == "" && groupAt >= 0:
		next = append(next[:groupAt], next[groupAt+1:]...)
	case group == "":
		return block, false // no group key, none wanted
	case groupAt >= 0:
		line := fmt.Sprintf("group = %s", strconv.Quote(group))
		if next[groupAt] == line {
			return block, false // already correct
		}
		next[groupAt] = line
	default:
		// Insert after `name` so a block always reads url/name/group.
		at := nameAt + 1
		line := fmt.Sprintf("group = %s", strconv.Quote(group))
		next = append(next[:at], append([]string{line}, next[at:]...)...)
	}
	return next, true
}

func key(line string) string {
	m := keyLine.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// stringValue extracts the quoted value of a `key = "value"` line.
func stringValue(line string) (string, bool) {
	_, rhs, ok := strings.Cut(line, "=")
	if !ok {
		return "", false
	}
	rhs = strings.TrimSpace(rhs)
	if i := strings.Index(rhs, " #"); i >= 0 {
		rhs = strings.TrimSpace(rhs[:i])
	}
	v, err := strconv.Unquote(rhs)
	if err != nil {
		return "", false
	}
	return v, true
}

// writeFileAtomic replaces path via a temp file + rename, so an
// interrupted write can't truncate a vault's only config.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(dirOf(path), ".hr.toml-*")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return "."
}
