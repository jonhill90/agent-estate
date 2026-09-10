package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultEndpoint is LM Studio's own default local server address --
// http://localhost:1234/v1, the OpenAI-compatible surface this package
// speaks. Never a remote default: agent-estate#1255's own brief is a local
// model only, and every caller that wants something else must say so
// explicitly via Config.Endpoint.
const DefaultEndpoint = "http://localhost:1234/v1"

// DefaultModel names the model agent-estate#1338/#1344 measured against
// and this feature was built for -- already present in LM Studio, per the
// brief's own "install nothing" constraint. A caller naming a different
// model gets that model's own dimension recorded in the cache instead;
// nothing here hardcodes the number 768 or any other dimension.
const DefaultModel = "text-embedding-nomic-embed-text-v1.5"

// DefaultTimeout bounds every embedding call this package makes. Deliberately
// short relative to the multi-minute bulk-embedding runs #1344's own
// experiment needed: a QUERY-time embedding is a single short string (the
// question), never the corpus -- see Client.Embed's own doc comment for why
// bulk corpus embedding never happens here at query time at all.
const DefaultTimeout = 10 * time.Second

// Config names one local embedding server and model. The zero value is
// not valid -- callers get it from NewConfig or construct Endpoint/Model
// explicitly; Validate reports why a Config cannot be used before any
// network call is attempted.
type Config struct {
	Endpoint string
	Model    string
	Timeout  time.Duration
}

// NewConfig returns the estate's own defaults -- local endpoint, the model
// already present in LM Studio, a bounded timeout. Every field is still a
// plain struct field a caller can override before calling Validate.
func NewConfig() Config {
	return Config{Endpoint: DefaultEndpoint, Model: DefaultModel, Timeout: DefaultTimeout}
}

// Validate checks the config is well-formed and local-only WITHOUT making
// a network call -- see validateEndpoint's own doc comment for what this
// does and does not catch (the real, authoritative check is the dialer in
// newLocalOnlyClient, run on every actual request).
func (c Config) Validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("embedding: empty endpoint")
	}
	if c.Model == "" {
		return fmt.Errorf("embedding: empty model")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("embedding: non-positive timeout %s", c.Timeout)
	}
	return validateEndpoint(c.Endpoint)
}

// Client talks to exactly one local embedding server, via a transport that
// refuses anything not loopback, any redirect, and any proxy.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient validates cfg and builds a Client, or returns the validation
// error -- never a Client that might later fail a check a caller could
// have been told about up front.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, httpClient: newLocalOnlyClient(cfg.Timeout)}, nil
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed sends texts to the configured local model in one request and
// returns one vector per input, in the same order. Never called with the
// whole corpus -- PrepareCache (cache.go) is the only caller that embeds
// many texts, and it runs explicitly via `estate knowledge embeddings`,
// never as a side effect of a query. A query-time caller embeds exactly
// one text: the question.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embeddingRequest{Model: c.cfg.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding endpoint %s: %w", c.cfg.Endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MiB cap -- a local model's own response, bounded rather than trusted unconditionally
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Never echo raw response bytes beyond a short, bounded prefix --
		// this can carry the server's own error detail, not corpus text
		// (only the QUESTION was ever sent), but keeping the transport's
		// own privacy discipline consistent throughout this package rather
		// than special-casing "this one is fine because it's only the
		// question" is the simpler, safer rule.
		return nil, fmt.Errorf("embedding endpoint %s returned %d: %s", c.cfg.Endpoint, resp.StatusCode, boundedPrefix(raw, 200))
	}
	var parsed embeddingResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embedding endpoint %s: malformed response: %w", c.cfg.Endpoint, err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding endpoint %s returned %d vector(s) for %d input(s)", c.cfg.Endpoint, len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("embedding endpoint %s returned an empty vector for input %d", c.cfg.Endpoint, i)
		}
		out[i] = d.Embedding
	}
	return out, nil
}

func boundedPrefix(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
