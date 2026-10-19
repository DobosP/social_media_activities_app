package web

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"sync"

	"github.com/flosch/pongo2/v6"
)

//go:embed pages/family-*.gohtml
var familyPages embed.FS

var loadFamilyTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("family").ParseFS(familyPages, "pages/family-*.gohtml")
})

func (r *Renderer) renderFamily(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	page, err := r.familyPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadFamilyTemplates()
	if err != nil {
		return nil, err
	}
	entry := "family-guardianship"
	if page.WardsPage {
		entry = "family-wards"
	}
	var output bytes.Buffer
	output.Grow(4096 + len(page.Wards)*8192 + len(page.Guardians)*2048)
	if err = parsed.ExecuteTemplate(&output, entry, page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
