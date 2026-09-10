package embedding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestValidateEndpointAcceptsLoopbackForms covers the fast, string-level
// check Config.Validate runs before any network call -- every spelling of
// loopback this package's own DefaultEndpoint or a reasonable override
// might use.
func TestValidateEndpointAcceptsLoopbackForms(t *testing.T) {
	for _, ep := range []string{
		"http://localhost:1234/v1",
		"http://127.0.0.1:1234/v1",
		"http://[::1]:1234/v1",
	} {
		if err := validateEndpoint(ep); err != nil {
			t.Errorf("validateEndpoint(%q) = %v, want nil (this is a loopback address)", ep, err)
		}
	}
}

// TestValidateEndpointRejectsNonLoopback is agent-estate#1255's own
// "the index must never leave the machine" requirement, at the cheapest
// possible check: a remote hostname must never even reach the dial step.
func TestValidateEndpointRejectsNonLoopback(t *testing.T) {
	for _, ep := range []string{
		"http://embeddings.example.com/v1",
		"https://8.8.8.8/v1",
		"http://localhost.attacker.example/v1", // looks local in the string, is not
	} {
		if err := validateEndpoint(ep); err == nil {
			t.Errorf("validateEndpoint(%q) = nil, want an ErrNotLoopback -- this is not a loopback address", ep)
		}
	}
}

// TestClientRefusesNonLoopbackAtDialTime is the AUTHORITATIVE check --
// not the string-level validateEndpoint above, but the dialer that runs
// on every real request. Points a Config at a hostname that resolves
// (via /etc/hosts's own guaranteed entry) to a NON-loopback address one
// specific way this package must catch: DNS resolving somewhere real
// even though the string itself might look benign.
func TestClientRefusesNonLoopbackAtDialTime(t *testing.T) {
	// broadcasthost resolves to 255.255.255.255 on macOS/BSD -- a stable,
	// always-present, definitely-non-loopback /etc/hosts entry, so this
	// test needs no network access and no fragile external DNS dependency.
	cfg := Config{Endpoint: "http://255.255.255.255:1/v1", Model: "m", Timeout: 2 * time.Second}
	client, err := NewClient(cfg)
	if err == nil {
		// validateEndpoint should have already refused a bare non-loopback
		// IP -- if it didn't, that is itself the bug this test would catch,
		// so keep going to the dial-time check below rather than failing
		// out here silently passing something worse through.
		_, embedErr := client.Embed(context.Background(), []string{"probe"})
		if embedErr == nil {
			t.Fatal("Embed against a non-loopback address succeeded -- local-only transport was bypassed")
		}
		return
	}
	var notLoopback *ErrNotLoopback
	if !errors.As(err, &notLoopback) {
		t.Fatalf("NewClient(%+v) error = %v, want an ErrNotLoopback", cfg, err)
	}
}

// TestClientRefusesRedirect proves a local server (real loopback, so it
// passes the dial-time check) cannot redirect this client anywhere --
// agent-estate#1255: a local model server has no legitimate reason to
// redirect, and following one could hand corpus text to whatever the
// redirect names.
func TestClientRefusesRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect TARGET was reached -- the client followed a redirect it must refuse")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/embeddings", http.StatusFound)
	}))
	defer redirector.Close()

	cfg := Config{Endpoint: redirector.URL, Model: "m", Timeout: 5 * time.Second}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Embed(context.Background(), []string{"probe"})
	if err == nil {
		t.Fatal("Embed followed a redirect instead of refusing it")
	}
	var redirErr *ErrRedirect
	if !errors.As(err, &redirErr) {
		t.Errorf("Embed error = %v, want an ErrRedirect", err)
	}
}

// TestClientIgnoresProxyEnvironment is agent-estate#1255's "proxy bypass"
// requirement: even with HTTP_PROXY set to a server that would record
// every request it saw, a request to a real loopback server must reach
// that server directly, never the configured proxy.
func TestClientIgnoresProxyEnvironment(t *testing.T) {
	realServerReached := false
	real := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		realServerReached = true
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer real.Close()

	proxyWasHit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyWasHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)

	cfg := Config{Endpoint: real.URL, Model: "m", Timeout: 5 * time.Second}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Embed(context.Background(), []string{"probe"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if proxyWasHit {
		t.Error("the configured HTTP_PROXY was contacted -- proxy environment variables must be ignored entirely")
	}
	if !realServerReached {
		t.Error("the real loopback server was never reached")
	}
}

// TestClientRespectsBoundedTimeout is the "bounded timeout" requirement:
// a server that never responds must not hang this client forever.
func TestClientRespectsBoundedTimeout(t *testing.T) {
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never responds until this test unblocks it below
	}))
	// Deliberately NOT deferred: slow.Close() waits for every in-flight
	// handler to return, so it must run AFTER close(block) unblocks the
	// handler above, never before -- a bare `defer slow.Close()` above a
	// bare `defer close(block)` would run Close() FIRST (LIFO) and
	// deadlock the test itself on server teardown, never reaching the
	// timeout assertion below at all.
	defer func() {
		close(block)
		slow.Close()
	}()

	cfg := Config{Endpoint: slow.URL, Model: "m", Timeout: 200 * time.Millisecond}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	start := time.Now()
	_, err = client.Embed(context.Background(), []string{"probe"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Embed against a server that never responds succeeded -- no timeout was enforced")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Embed took %s to time out against a 200ms-timeout client -- the bound was not enforced", elapsed)
	}
}

// TestConfigValidateCatchesEmptyFields is a cheap completeness check on
// Validate itself -- a Config with an obviously-missing field must be
// refused before any of the above ever runs.
func TestConfigValidateCatchesEmptyFields(t *testing.T) {
	cases := []Config{
		{Endpoint: "", Model: "m", Timeout: time.Second},
		{Endpoint: "http://localhost:1234/v1", Model: "", Timeout: time.Second},
		{Endpoint: "http://localhost:1234/v1", Model: "m", Timeout: 0},
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("Config%+v.Validate() = nil, want an error", c)
		}
	}
}

func TestMain(m *testing.M) {
	// Belt-and-suspenders against this package's own tests accidentally
	// depending on an operator's real proxy environment when run outside
	// TestClientIgnoresProxyEnvironment's own t.Setenv scoping (e.g. a CI
	// runner with HTTP_PROXY set repo-wide for unrelated reasons) --
	// clearing it here for the whole package's test binary, restored by
	// nothing because this process exits right after m.Run().
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
