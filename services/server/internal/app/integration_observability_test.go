package app

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestNativeRealHTTPProxyTransportRequiresConfiguredPeer(t *testing.T) {
	for _, test := range []struct {
		name, network string
		status        int
		hsts          bool
	}{
		{"trusted-loopback", "127.0.0.1/32", 204, true},
		{"untrusted-loopback", "192.0.2.0/24", 301, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := &App{Config: Config{PublicURL: "https://social.fixture.test", AllowedHosts: []string{"social.fixture.test"}, SecureSSLRedirect: true, HSTSSeconds: 31536000}, proxyNetworks: []netip.Prefix{netip.MustParsePrefix(test.network)}, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
			server := httptest.NewServer(a)
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			req, err := http.NewRequest("GET", server.URL+"/static/css/base.css", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "social.fixture.test"
			req.Header.Set("X-Forwarded-Proto", "https")
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status || (response.Header.Get("Strict-Transport-Security") != "") != test.hsts {
				t.Fatal("real proxy HTTP transport lost TLS boundary", response.StatusCode, response.Header.Get("Strict-Transport-Security"))
			}
			if test.status == 301 && response.Header.Get("Location") != "https://social.fixture.test/static/css/base.css" {
				t.Fatal("untrusted protocol header changed canonical redirect")
			}
		})
	}
}
