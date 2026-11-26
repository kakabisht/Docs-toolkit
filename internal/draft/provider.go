package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Provider writes section text from a prompt. It is optional: without one,
// drafts are extractive.
type Provider interface {
	Name() string // for the front matter and ledger, e.g. "ollama llama3.1"
	Complete(ctx context.Context, system, prompt string) (string, error)
}

// ProviderConfig selects and configures a provider.
type ProviderConfig struct {
	Kind        string        `yaml:"provider"`     // none (default), ollama, openai, anthropic
	Model       string        `yaml:"model"`        //
	Endpoint    string        `yaml:"endpoint"`     // base URL; ollama and openai-compatible servers
	APIKeyEnv   string        `yaml:"api_key_env"`  // environment variable holding the key
	AllowRemote bool          `yaml:"allow_remote"` // needed to send content anywhere but this machine
	Timeout     time.Duration `yaml:"timeout"`
}

// NewProvider returns the configured provider, or nil for none. It refuses to
// send repository content off this machine unless AllowRemote is set.
func NewProvider(c ProviderConfig) (Provider, error) {
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Minute
	}
	key := ""
	if c.APIKeyEnv != "" {
		key = os.Getenv(c.APIKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("%s is not set", c.APIKeyEnv)
		}
	}
	switch strings.ToLower(c.Kind) {
	case "", "none":
		return nil, nil
	case "ollama":
		ep := firstNonEmpty(c.Endpoint, "http://localhost:11434")
		if err := localOnly(ep, c.AllowRemote); err != nil {
			return nil, err
		}
		if c.Model == "" {
			return nil, fmt.Errorf("ollama: set a model (for example draft.model: llama3.1)")
		}
		return &ollama{ep: strings.TrimRight(ep, "/"), model: c.Model, http: &http.Client{Timeout: c.Timeout}}, nil
	case "openai", "openai-compatible":
		if c.Endpoint == "" {
			return nil, fmt.Errorf("openai-compatible: set endpoint (for example http://localhost:8080/v1 for llama.cpp or LM Studio)")
		}
		if err := localOnly(c.Endpoint, c.AllowRemote); err != nil {
			return nil, err
		}
		if c.Model == "" {
			return nil, fmt.Errorf("openai-compatible: set a model")
		}
		return &openAI{ep: strings.TrimRight(c.Endpoint, "/"), model: c.Model, key: key, http: &http.Client{Timeout: c.Timeout}}, nil
	case "anthropic":
		if !c.AllowRemote {
			return nil, fmt.Errorf("anthropic sends the source passages to Anthropic's API; set draft.allow_remote: true (or --allow-remote) to allow it")
		}
		opts := []option.RequestOption{option.WithRequestTimeout(c.Timeout)}
		if key != "" {
			opts = append(opts, option.WithAPIKey(key))
		}
		if c.Endpoint != "" {
			opts = append(opts, option.WithBaseURL(c.Endpoint))
		}
		return &claude{client: anthropic.NewClient(opts...), model: firstNonEmpty(c.Model, "claude-opus-5-5")}, nil
	}
	return nil, fmt.Errorf("unknown provider %q: want none, ollama, openai or anthropic", c.Kind)
}

// localOnly refuses endpoints that are not on this machine unless allowed.
func localOnly(endpoint string, allow bool) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("endpoint %q is not a URL", endpoint)
	}
	if allow {
		return nil
	}
	h := u.Hostname()
	if h == "localhost" {
		return nil
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("endpoint %s is not on this machine; the draft would send source passages there. Set draft.allow_remote: true (or --allow-remote) if that is intended", h)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func postJSON(ctx context.Context, c *http.Client, url, key string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s: %s", url, resp.Status, strings.TrimSpace(string(rb[:min(len(rb), 300)])))
	}
	return json.Unmarshal(rb, out)
}

type ollama struct {
	ep, model string
	http      *http.Client
}

func (o *ollama) Name() string { return "ollama " + o.model }

func (o *ollama) Complete(ctx context.Context, system, prompt string) (string, error) {
	var out struct {
		Message struct{ Content string } `json:"message"`
	}
	err := postJSON(ctx, o.http, o.ep+"/api/chat", "", map[string]any{
		"model": o.model, "stream": false,
		"options":  map[string]any{"temperature": 0, "seed": 1},
		"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": prompt}},
	}, &out)
	return out.Message.Content, err
}

type openAI struct {
	ep, model, key string
	http           *http.Client
}

func (o *openAI) Name() string { return "openai-compatible " + o.model }

func (o *openAI) Complete(ctx context.Context, system, prompt string) (string, error) {
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	err := postJSON(ctx, o.http, o.ep+"/chat/completions", o.key, map[string]any{
		"model": o.model, "temperature": 0, "seed": 1,
		"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": prompt}},
	}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s: no choices in the response", o.ep)
	}
	return out.Choices[0].Message.Content, nil
}

type claude struct {
	client anthropic.Client
	model  string
}

func (c *claude) Name() string { return "anthropic " + c.model }

func (c *claude) Complete(ctx context.Context, system, prompt string) (string, error) {
	resp, err := c.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		// a refused request is retried on a fallback model chosen by the API
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
	})
	if err != nil {
		return "", err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", fmt.Errorf("anthropic: the model declined this section")
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	if resp.StopReason == anthropic.BetaStopReasonMaxTokens {
		return "", fmt.Errorf("anthropic: the response was cut off at the token limit")
	}
	return b.String(), nil
}
