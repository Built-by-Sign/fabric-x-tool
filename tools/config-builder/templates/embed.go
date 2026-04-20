package templates

import (
	"embed"
	"path"
	"text/template"
)

// FS contains vendored non-Kubernetes config templates aligned with the
// official Fabric-X Ansible collection, adapted from Jinja to Go templates.
//
//go:embed armageddon/*.tmpl committer/*.tmpl committer/sections/*.tmpl crypto/*.tmpl genesis/*.tmpl orderer/*.tmpl orderer/sections/*.tmpl
var FS embed.FS

// Parse loads a main template and any supporting templates from the embedded FS.
// The main template is parsed last so Execute renders the requested file.
func Parse(mainPath string, funcs template.FuncMap, supportingPaths ...string) (*template.Template, error) {
	paths := make([]string, 0, len(supportingPaths)+1)
	paths = append(paths, supportingPaths...)
	paths = append(paths, mainPath)

	tmpl := template.New(path.Base(mainPath))
	if funcs != nil {
		tmpl = tmpl.Funcs(funcs)
	}
	return tmpl.ParseFS(FS, paths...)
}
