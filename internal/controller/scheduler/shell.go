package scheduler

import (
	"bytes"
	"text/template"
)

var createUserScriptTmpl = template.Must(template.New("createUser").Parse(`# Portable user/group creation
if command -v adduser >/dev/null 2>&1 && adduser --help 2>&1 | grep -q BusyBox; then
{{- if .CreateGroup }}
    addgroup -g {{.GID}} {{.Groupname}} 2>/dev/null || true
{{- end }}
    adduser -D -u {{.UID}} -G {{.Groupname}} -h /workspace {{.Username}}
elif command -v useradd >/dev/null 2>&1; then
{{- if .CreateGroup }}
    groupadd -g {{.GID}} {{.Groupname}} 2>/dev/null || true
{{- end }}
    useradd -M -u {{.UID}} -g {{.Groupname}} -d /workspace -s /bin/sh {{.Username}}
else
    echo "Error: No supported user creation tool found" >&2
    exit 1
fi
`))

// generateCreateUserScript generates a shell script that creates a user
// and optionally a group, working on both Alpine (BusyBox) and glibc-based
// systems (Debian, Ubuntu, RHEL).
func generateCreateUserScript(uid, gid int64, username, groupname string) string {
	data := struct {
		UID         int64
		GID         int64
		Username    string
		Groupname   string
		CreateGroup bool
	}{
		UID:         uid,
		GID:         gid,
		Username:    username,
		Groupname:   groupname,
		CreateGroup: groupname != "" && groupname != "root",
	}

	var buf bytes.Buffer
	_ = createUserScriptTmpl.Execute(&buf, data)
	return buf.String()
}
