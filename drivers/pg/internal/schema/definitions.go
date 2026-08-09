package schema

import (
	"embed"
	"fmt"
	"strings"
)

// assets is the sole embedded owner of canonical runtime and migration schema
// DDL. Callers receive immutable strings and cannot maintain a second copy.
//
//go:embed sql
var assets embed.FS

func load(name string) string {
	content, err := assets.ReadFile("sql/" + name)
	if err != nil {
		panic(fmt.Sprintf("unable to load canonical schema asset %s: %v", name, err))
	}
	builder := strings.Builder{}
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		builder.WriteString(trimmed)
		builder.WriteByte('\n')
	}
	return builder.String()
}

var (
	up   = load("schema_up.sql")
	down = load("schema_down.sql")
)

func Up() string   { return up }
func Down() string { return down }
