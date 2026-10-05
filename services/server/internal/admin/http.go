package admin

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

type CSRF interface {
	EnsureCSRF(http.ResponseWriter, *http.Request) string
	CheckCSRF(*http.Request) error
}
type HTTP struct {
	Service *Service
	Auth    CSRF
}

var page = template.Must(template.New("admin").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Operations</title><main><h1>Operations</h1><p><a href="/admin/">Model index</a> · <a href="/moderation/">Moderation queue</a></p>{{if .Message}}<p role="status">{{.Message}}</p>{{end}}{{if .Models}}<ul>{{range .Models}}<li><a href="/admin/{{.Name}}/">{{.Name}}</a>{{if .Editable}} — curated fields{{end}}</li>{{end}}</ul>{{else}}<h2>{{.Model}}</h2><pre>{{.Rows}}</pre>{{if .Actions}}<form method="post"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRF}}"><label>Action<select name="action">{{range .Actions}}<option value="{{.}}">{{.}}</option>{{end}}</select></label><label>Selected record IDs<input name="ids" required placeholder="1,2"></label><label>Review notes<textarea name="reason" maxlength="2000"></textarea></label><button>Apply reviewed action</button></form>{{end}}{{if .Permissions}}<h3>Platform permissions</h3><p>Grants require a verified adult. Changes revoke existing sessions and API tokens. A permission change cannot remove the last administrator.</p><form method="post"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRF}}"><input type="hidden" name="operation" value="permissions"><label>Account record ID<input type="number" name="id" min="1" required></label><label>Permission level<select name="level"><option value="user">User — ordinary account</option><option value="moderator">Moderator — moderation console</option><option value="operator">Operator — in-app staff tools and moderation, no console</option><option value="administrator">Administrator — console and permission management</option></select></label><label>Review reason<textarea name="reason" maxlength="2000" required></textarea></label><button>Apply reviewed permission change</button></form>{{end}}{{if .Editable}}<h3>Curated data</h3><p>Allowed fields and types:</p><pre>{{.Fields}}</pre><form method="post"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRF}}"><input type="hidden" name="operation" value="save"><label>Record ID (0 creates a record)<input type="number" name="id" value="0" min="0" required></label><label>Fields (JSON object)<textarea name="fields" rows="12" cols="72" required></textarea></label><button>Save curated data</button></form>{{end}}{{end}}</main></html>`))

func (h HTTP) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/{$}", h.ServeHTTP)
	mux.HandleFunc("GET /admin/{model}/{$}", h.ServeHTTP)
	mux.HandleFunc("POST /admin/{model}/{$}", h.ServeHTTP)
	mux.HandleFunc("GET /admin/{app}/{model}/{$}", h.ServeHTTP)
	mux.HandleFunc("POST /admin/{app}/{model}/{$}", h.ServeHTTP)
}
func (h HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.ActorFrom(r)
	if !ok || h.Service.Gate(r.Context(), a) != nil {
		http.NotFound(w, r)
		return
	}
	model := r.PathValue("model")
	if app := r.PathValue("app"); app != "" {
		model = app + "." + model
	}
	csrf := ""
	if h.Auth != nil {
		csrf = h.Auth.EnsureCSRF(w, r)
	}
	data := map[string]any{"Model": model, "CSRF": csrf}
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if r.ParseForm() != nil {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		if h.Auth == nil {
			platform.Fail(w, platform.ErrForbidden)
			return
		}
		copy := r.Clone(r.Context())
		copy.Header = r.Header.Clone()
		copy.Header.Set("X-CSRFToken", r.PostForm.Get("csrfmiddlewaretoken"))
		if h.Auth.CheckCSRF(copy) != nil {
			platform.Error(w, 403, "CSRF verification failed.")
			return
		}
		if r.PostForm.Get("operation") == "permissions" {
			for key, values := range r.PostForm {
				if len(values) != 1 || (key != "operation" && key != "id" && key != "level" && key != "reason" && key != "csrfmiddlewaretoken") {
					platform.Fail(w, platform.ErrInvalid)
					return
				}
			}
			id, err := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)
			if model != "accounts.user" || err != nil {
				platform.Fail(w, platform.ErrInvalid)
				return
			}
			if err = h.Service.ChangePermissions(r.Context(), a, id, PermissionLevel(r.PostForm.Get("level")), r.PostForm.Get("reason")); err != nil {
				platform.Fail(w, err)
				return
			}
			// Self-revocation also revokes this request's credential. A fresh GET
			// must reload authority rather than render another model projection.
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
			return
		} else if r.PostForm.Get("operation") == "save" {
			id, err := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)
			fields := map[string]json.RawMessage{}
			if err != nil || json.Unmarshal([]byte(r.PostForm.Get("fields")), &fields) != nil {
				platform.Fail(w, platform.ErrInvalid)
				return
			}
			result, err := h.Service.Save(r.Context(), a, model, id, fields)
			if err != nil {
				platform.Fail(w, err)
				return
			}
			data["Message"] = "Saved record " + strconv.FormatInt(result, 10) + "."
		} else {
			ids := []int64{}
			rawIDs := r.PostForm["_selected_action"]
			if len(rawIDs) == 0 {
				rawIDs = strings.Split(r.PostForm.Get("ids"), ",")
			}
			for _, raw := range rawIDs {
				id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil {
					platform.Fail(w, platform.ErrInvalid)
					return
				}
				ids = append(ids, id)
			}
			results, err := h.Service.Execute(r.Context(), a, model, r.PostForm.Get("action"), ids, r.PostForm.Get("reason"))
			if err != nil {
				platform.Fail(w, err)
				return
			}
			raw, _ := json.Marshal(results)
			data["Message"] = string(raw)
		}
	}
	if model == "" {
		models, err := h.Service.Models(r.Context(), a)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		data["Models"] = models
	} else {
		m, ok := models[model]
		if !ok {
			http.NotFound(w, r)
			return
		}
		rows, err := h.Service.List(r.Context(), a, model, 200, 0)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		raw, _ := json.MarshalIndent(rows, "", "  ")
		data["Rows"], data["Actions"], data["Editable"] = string(raw), m.Actions, m.Editable
		fields, _ := json.MarshalIndent(EditableFields(model), "", "  ")
		data["Fields"] = string(fields)
		data["Permissions"] = model == "accounts.user" && h.Service.PermissionGate(r.Context(), a) == nil
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := page.Execute(w, data); err != nil {
		return
	}
}
