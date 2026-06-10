package gitops

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"text/template"

	opsruntime "github.com/winshare/zeroops/internal/shared/runtime"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

type renderTemplateData struct {
	TeamSlug    string
	AppSlug     string
	ImageRef    string
	PrimaryPort int
	DomainBase  string
}

// Render renders the create_app manifest set.
func (s *service) Render(ctx context.Context, input RenderInput) (RenderResult, error) {
	_ = ctx
	files := map[string]string{}
	data := renderTemplateData{
		TeamSlug:    input.TeamSlug,
		AppSlug:     input.AppSlug,
		ImageRef:    input.ImageRef,
		PrimaryPort: input.PrimaryPort,
		DomainBase:  opsruntime.DomainBase(),
	}

	for _, item := range []struct {
		path string
		name string
	}{
		{path: fmt.Sprintf("apps/%s/%s/deployment.yaml", input.TeamSlug, input.AppSlug), name: "deployment.yaml.tmpl"},
		{path: fmt.Sprintf("apps/%s/%s/service.yaml", input.TeamSlug, input.AppSlug), name: "service.yaml.tmpl"},
		{path: fmt.Sprintf("apps/%s/%s/ingress.yaml", input.TeamSlug, input.AppSlug), name: "ingress.yaml.tmpl"},
		{path: fmt.Sprintf("apps/%s/%s/kustomization.yaml", input.TeamSlug, input.AppSlug), name: "app-kustomization.yaml.tmpl"},
		{path: fmt.Sprintf("teams/%s/namespace.yaml", input.TeamSlug), name: "namespace.yaml.tmpl"},
		{path: fmt.Sprintf("teams/%s/kustomization.yaml", input.TeamSlug), name: "team-kustomization.yaml.tmpl"},
	} {
		rendered, err := executeTemplate(item.name, data)
		if err != nil {
			return RenderResult{}, err
		}
		files[item.path] = rendered
	}

	return RenderResult{Files: files}, nil
}

func executeTemplate(name string, data renderTemplateData) (string, error) {
	raw, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return "", err
	}
	tpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
