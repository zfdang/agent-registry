package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"
)

// permissiveGuard refuses nothing — these tests exercise redirect handling
// against httptest servers on 127.0.0.1, which the production AddressGuard
// would (correctly) refuse as loopback.
type permissiveGuard struct{}

func (permissiveGuard) CheckHost(string) error                                  { return nil }
func (permissiveGuard) CheckDial(string, string, string, syscall.RawConn) error { return nil }

func newTestClient(pool *x509.CertPool) *Client {
	return NewClient(WithGuard(permissiveGuard{}), WithTLSConfig(&tls.Config{RootCAs: pool}))
}

// TestClient_SameOriginRedirectAllowed confirms ordinary same-origin
// redirects (e.g. a canonicalizing 301) still work.
func TestClient_SameOriginRedirectAllowed(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ts.URL+"/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	client := newTestClient(pool)

	res, err := client.Get(context.Background(), ts.URL+"/start")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.StatusCode != 200 || string(res.Body) != "ok" {
		t.Fatalf("got status=%d body=%q, want 200/ok", res.StatusCode, res.Body)
	}
	if res.FinalURL != ts.URL+"/final" {
		t.Errorf("FinalURL = %q, want %s/final", res.FinalURL, ts.URL)
	}
}

// TestClient_CrossOriginRedirectRefused is the regression test for P1-4:
// L1's third discovery candidate is the caller-supplied URL itself, so an
// open redirect there must not be able to hand the fetch (and L3's POST,
// since 307/308 preserve method+body) to a different origin.
func TestClient_CrossOriginRedirectRefused(t *testing.T) {
	targetMux := http.NewServeMux()
	target := httptest.NewTLSServer(targetMux)
	defer target.Close()
	targetMux.HandleFunc("/attacker-card", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"attacker"}`))
	})

	originMux := http.NewServeMux()
	origin := httptest.NewTLSServer(originMux)
	defer origin.Close()
	originMux.HandleFunc("/open-redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/attacker-card", http.StatusFound)
	})

	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	pool.AddCert(target.Certificate())
	client := newTestClient(pool)

	_, err := client.Get(context.Background(), origin.URL+"/open-redirect")
	if err == nil {
		t.Fatal("Get across a cross-origin redirect succeeded, want refusal")
	}
}

// TestClient_TooManyRedirectsRefused confirms the redirect count cap still
// applies alongside the new same-origin check.
func TestClient_TooManyRedirectsRefused(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	for i := 0; i < 5; i++ {
		i := i
		mux.HandleFunc(hop(i), func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, ts.URL+hop(i+1), http.StatusFound)
		})
	}

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	client := newTestClient(pool)

	_, err := client.Get(context.Background(), ts.URL+hop(0))
	if err == nil {
		t.Fatal("Get through 5 redirects succeeded, want refusal (max 3)")
	}
}

func hop(n int) string {
	return "/hop" + string(rune('0'+n))
}

// TestSameOrigin_DefaultPortNormalized is the regression test for the
// review finding that CheckRedirect used to compare raw "scheme://host"
// strings: an explicit default port (https example.com:443) must be
// treated as the same origin as the implicit form (https example.com),
// the same way pkg/service's SameOrigin already does — otherwise a
// redirect to the explicit form would be wrongly refused as cross-origin.
func TestSameOrigin_DefaultPortNormalized(t *testing.T) {
	implicit, _ := url.Parse("https://example.com/x")
	explicit, _ := url.Parse("https://example.com:443/x")
	if !sameOrigin(implicit, explicit) {
		t.Error("sameOrigin(https://example.com, https://example.com:443) = false, want true")
	}

	httpImplicit, _ := url.Parse("http://example.com/x")
	httpExplicit, _ := url.Parse("http://example.com:80/x")
	if !sameOrigin(httpImplicit, httpExplicit) {
		t.Error("sameOrigin(http://example.com, http://example.com:80) = false, want true")
	}

	differentPort, _ := url.Parse("https://example.com:8443/x")
	if sameOrigin(implicit, differentPort) {
		t.Error("sameOrigin(https://example.com, https://example.com:8443) = true, want false")
	}
}
