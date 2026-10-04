package authcore

import (
	"html/template"
	"net/http"
	"strings"
)

// LoginPage provides a small native browser flow shared by both applications.
// Provider credentials and tokens never enter the page or its JavaScript.
func (s *Service) LoginPage(title, home string) http.Handler {
	page := template.Must(template.New("login").Parse(loginHTML))
	if !strings.HasPrefix(home, "/") || strings.HasPrefix(home, "//") || strings.ContainsAny(home, "\\\r\n") {
		home = "/"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			fail(w, 405, "GET required")
			return
		}
		csrf := s.EnsureCSRF(w, r)
		nonce := randomToken()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; style-src 'nonce-"+nonce+"'; script-src 'nonce-"+nonce+"'")
		_ = page.Execute(w, map[string]any{"Title": title, "Home": home, "Nonce": nonce, "CSRF": csrf, "Google": s.providerEnabled("google"), "Facebook": s.providerEnabled("facebook")})
	})
}

const loginHTML = `<!doctype html><html lang="ro"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Intră în cont · {{.Title}}</title>
<style nonce="{{.Nonce}}">*{box-sizing:border-box}body{margin:0;background:#f3f5f3;color:#17211e;font:17px/1.5 system-ui,sans-serif}main{max-width:480px;margin:8vh auto;padding:28px;background:white;border:1px solid #d7dfd8;border-radius:18px}h1{font-size:28px;margin:0 0 8px}p{margin:8px 0 20px}label{display:block;margin:14px 0 5px}input,button,a.provider{width:100%;padding:12px;border:1px solid #9caaa0;border-radius:9px;font:inherit}button{margin-top:16px;background:#195c43;color:white;cursor:pointer}a{color:#195c43}a.provider{display:block;text-align:center;text-decoration:none;margin:10px 0}fieldset{border:0;padding:0}.choice{display:flex;gap:16px;margin:18px 0}.choice label{margin:0}.choice input{width:auto}#message{min-height:24px;color:#8e231b}</style></head><body><main><h1>Intră în cont</h1><p>{{.Title}}</p>
{{if .Google}}<a class="provider" href="/api/auth/oauth/google/start">Continuă cu Google</a>{{end}}{{if .Facebook}}<a class="provider" href="/api/auth/oauth/facebook/start">Continuă cu Facebook</a>{{end}}
<form id="account" autocomplete="on"><fieldset><legend>Folosește numele de utilizator și parola</legend><div class="choice"><label><input type="radio" name="mode" value="login" checked> Autentificare</label><label><input type="radio" name="mode" value="signup"> Cont nou</label></div><label for="username">Nume de utilizator</label><input id="username" name="username" required minlength="3" maxlength="150" autocomplete="username"><label for="password">Parolă</label><input id="password" name="password" type="password" required maxlength="1024" autocomplete="current-password"><button id="submit" type="submit">Continuă</button></fieldset></form><p id="message" role="status" aria-live="polite"></p><a href="{{.Home}}">Înapoi</a></main>
<script nonce="{{.Nonce}}">const form=document.getElementById('account'),message=document.getElementById('message'),button=document.getElementById('submit'),password=document.getElementById('password');form.addEventListener('change',()=>{password.autocomplete=form.elements.mode.value==='signup'?'new-password':'current-password'});form.addEventListener('submit',async(event)=>{event.preventDefault();button.disabled=true;message.textContent='';try{const response=await fetch('/api/auth/'+form.elements.mode.value,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRFToken':{{.CSRF}}},body:JSON.stringify({username:form.elements.username.value,password:password.value})});if(response.ok){window.location.assign({{.Home}});return}message.textContent=response.status===429?'Prea multe încercări. Încearcă din nou mai târziu.':form.elements.mode.value==='signup'?'Contul nu a putut fi creat. Verifică numele și folosește o parolă de cel puțin 12 caractere.':'Numele de utilizator sau parola nu sunt corecte.'}catch{message.textContent='Nu ne putem conecta acum. Încearcă din nou.'}finally{button.disabled=false}})</script></body></html>`
