package web

import (
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// Documentation exposes contract metadata only. These handlers do not load an
// account, database row or session and use the existing public download headers.
func (s *Server) OpenAPIDocument(w http.ResponseWriter, r *http.Request) {
	s.PublicDownload(w, r, platform.Actor{}, "openapi_schema")
}

func (s *Server) APIDocumentation(w http.ResponseWriter, r *http.Request) {
	s.PublicDownload(w, r, platform.Actor{}, "api_docs")
}
