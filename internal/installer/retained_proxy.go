package installer

import (
	"os"
	"path/filepath"
)

func (r *Real) retainedDockerStack() bool {
	marker, err := os.ReadFile(filepath.Join(r.Root, "retained-docker-proxy"))
	if err != nil || string(marker) != "managed\n" {
		return false
	}
	if noSymlinkParents(r.Stack) != nil {
		return false
	}
	entries, err := os.ReadDir(r.Stack)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || (entry.Name() != "caddy" && entry.Name() != "nginx") {
			return false
		}
		children, e := os.ReadDir(filepath.Join(r.Stack, entry.Name()))
		if e != nil {
			return false
		}
		for _, child := range children {
			if entry.Name() == "nginx" && (child.Name() == "fullchain.pem" || child.Name() == "privkey.key") {
				info, e := child.Info()
				if e != nil || !info.Mode().IsRegular() {
					return false
				}
				continue
			}
			if !child.IsDir() {
				return false
			}
			if entry.Name() == "caddy" && child.Name() != "data" && child.Name() != "config" {
				return false
			}
			if entry.Name() == "nginx" && child.Name() != "certs" && child.Name() != "acme" {
				return false
			}
		}
	}
	return true
}
