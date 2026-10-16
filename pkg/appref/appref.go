// defines the convention mapping a c12s app (org/namespace/app) to a container name and back
package appref

import "strings"

const sep = "_"

// encodes an app identity into a container name
func ContainerName(org, namespace, app string) string {
	return org + sep + namespace + sep + app
}

// decodes a container name back into its app identity
func Parse(containerName string) (org, namespace, app string, ok bool) {
	parts := strings.SplitN(containerName, sep, 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// returns the "org/namespace/app" id for a container name or "" if the name does not follow the convention
func AppID(containerName string) string {
	org, namespace, app, ok := Parse(containerName)
	if !ok {
		return ""
	}
	return org + "/" + namespace + "/" + app
}
