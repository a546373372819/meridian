package domain

import "strings"


//reports whether id names an app (org/ns/app).
func IsAppId(id string) bool {
	return strings.Count(id, "/") == 2
}

func SplitAppId(id string) (org, namespace, app string, ok bool) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 || !IsAppId(id) {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func OrgOf(id string) string {
	if i := strings.Index(id, "/"); i >= 0 {
		return id[:i]
	}
	return id
}

func NamespaceOf(id string) string {
	if IsAppId(id) {
		return id[:strings.LastIndex(id, "/")]
	}
	return id
}
