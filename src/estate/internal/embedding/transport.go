// Package embedding is the local-only adapter for agent-estate#1255's
// optional semantic reranker: a real embedding model, reachable ONLY on
// loopback, whose corpus-side vectors are prepared once and cached, and
// whose failure at query time falls back to BM25 exactly, never partially.
//
// Nothing in this package embeds the corpus at query time, follows a
// redirect, honours an environment proxy, or sends anything to a
// non-loopback host -- see newLocalOnlyClient's own doc comment for what
// "local-only" means operationally, not just by convention.
package embedding

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotLoopback is returned by newLocalOnlyClient (via its RoundTrip
// wrapper) when a request's own host does not resolve to a loopback
// address. Checked at DIAL time, not just by inspecting the URL string --
// a hostname that LOOKS local ("localhost.attacker.example") but resolves
// elsewhere must refuse exactly the same as an address that is honestly
// remote.
type ErrNotLoopback struct {
	Host string
}

func (e *ErrNotLoopback) Error() string {
	return fmt.Sprintf("embedding endpoint host %q did not resolve to a loopback address -- refusing to send corpus text off this machine (agent-estate#1255)", e.Host)
}

// ErrRedirect is returned when the embedding server's response tried to
// redirect the request elsewhere. A local model server has no legitimate
// reason to redirect; following one could hand corpus text to whatever the
// redirect names.
type ErrRedirect struct {
	Location string
}

func (e *ErrRedirect) Error() string {
	return fmt.Sprintf("embedding endpoint attempted to redirect to %q -- refusing (agent-estate#1255: local-only transport)", e.Location)
}

// newLocalOnlyClient builds an *http.Client hardened for exactly one job:
// talking to a local embedding server and nothing else.
//
//   - Proxy: nil -- HTTP_PROXY/HTTPS_PROXY/NO_PROXY and any other
//     environment-derived proxy configuration are never consulted. Without
//     this, http.DefaultTransport's ProxyFromEnvironment would route a
//     "loopback" request through an operator's configured proxy, which is
//     exactly the leak this package exists to prevent.
//   - DialContext verifies the resolved IP is loopback (127.0.0.0/8 or ::1)
//     BEFORE connecting -- not a check on the URL's hostname string, which
//     tells you nothing about where a name actually resolves.
//   - CheckRedirect refuses unconditionally: any 3xx is treated as a
//     transport failure, never followed.
//   - Timeout is bounded and always set by the caller (see Config.Timeout);
//     this constructor never supplies its own default so a caller cannot
//     forget to think about it.
func newLocalOnlyClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: nil, // never consult HTTP_PROXY/HTTPS_PROXY/NO_PROXY
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			loopback := false
			for _, ip := range ips {
				if ip.IP.IsLoopback() {
					loopback = true
					break
				}
			}
			if !loopback {
				return nil, &ErrNotLoopback{Host: host}
			}
			// Dial the loopback IP actually resolved, not the original
			// hostname string again -- avoids a second, potentially
			// different resolution (DNS rebinding) between the check above
			// and the connection itself.
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			loc := ""
			if req.Response != nil {
				loc = req.Response.Header.Get("Location")
			} else if req.URL != nil {
				loc = req.URL.String()
			}
			return &ErrRedirect{Location: loc}
		},
	}
}

// validateEndpoint does the same loopback check as the dialer above,
// purely so a caller (Config.Validate) can fail fast on an obviously wrong
// endpoint -- "https://embeddings.example.com/v1" -- before any network
// call, with a clearer message than a mid-dial refusal would give. The
// dialer's own check is what actually enforces this; this is a fast,
// friendlier first line, not a replacement for it (this function does not
// re-resolve DNS, so it cannot catch DNS rebinding -- the dialer does).
func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("embedding: invalid endpoint %q: %w", endpoint, err)
	}
	// url.URL.Hostname() is bracket-aware -- "[::1]:1234" correctly yields
	// "::1", not "[" (a naive IndexAny(":") split, tried first and
	// rejected, cuts an IPv6 literal at its FIRST colon, inside the
	// brackets).
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return &ErrNotLoopback{Host: host}
}
