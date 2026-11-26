// Package fetch is a polite HTTP client: per-host rate limits, a global
// concurrency cap, manual redirect tracking and an on-disk response cache.
package fetch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Options configures a Client.
type Options struct {
	Concurrency  int
	Delay        time.Duration // minimum gap between requests to one host
	Timeout      time.Duration
	UserAgent    string
	CacheDir     string // empty disables the cache
	CacheTTL     time.Duration
	MaxRedirects int
	MaxBody      int64
}

// Hop is one step in a redirect chain.
type Hop struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
}

// Response is the outcome of a request after following redirects.
type Response struct {
	URL         string    `json:"url"`       // requested URL
	FinalURL    string    `json:"final_url"` // URL after redirects
	Status      int       `json:"status"`    // final status, 0 on network error
	ContentType string    `json:"content_type,omitempty"`
	Chain       []Hop     `json:"chain,omitempty"` // redirect hops, empty when none
	Body        []byte    `json:"body,omitempty"`
	Err         string    `json:"error,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
	RetryAfter  string    `json:"-"`
}

// Redirected reports whether the request was redirected.
func (r *Response) Redirected() bool { return len(r.Chain) > 0 }

// OK reports a 2xx final status.
func (r *Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Client is safe for concurrent use.
type Client struct {
	opt  Options
	http *http.Client
	sem  chan struct{}

	mu    sync.Mutex
	hosts map[string]*hostGate
}

type hostGate struct {
	mu   sync.Mutex
	next time.Time
}

// New returns a Client.
func New(opt Options) *Client {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 8
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 20 * time.Second
	}
	if opt.MaxRedirects <= 0 {
		opt.MaxRedirects = 10
	}
	if opt.MaxBody <= 0 {
		opt.MaxBody = 20 << 20
	}
	if opt.UserAgent == "" {
		opt.UserAgent = "docsbot/0.1"
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = opt.Concurrency
	return &Client{
		opt: opt,
		http: &http.Client{
			Transport: tr,
			Timeout:   opt.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		sem:   make(chan struct{}, opt.Concurrency),
		hosts: map[string]*hostGate{},
	}
}

// Get fetches u with its body. Cached responses younger than CacheTTL are reused.
func (c *Client) Get(ctx context.Context, u string) *Response {
	return c.do(ctx, http.MethodGet, u, true)
}

// Check fetches u without keeping the body: HEAD first, falling back to GET
// when the server rejects HEAD.
func (c *Client) Check(ctx context.Context, u string) *Response {
	r := c.do(ctx, http.MethodHead, u, false)
	switch r.Status {
	case 0, 400, 403, 404, 405, 406, 429, 500, 501, 503:
		// Many servers mishandle HEAD; confirm with GET before calling it broken.
		g := c.do(ctx, http.MethodGet, u, false)
		if g.Status != 0 || r.Status == 0 {
			return g
		}
	}
	return r
}

func (c *Client) do(ctx context.Context, method, u string, keepBody bool) *Response {
	key := method + " " + u
	if !keepBody {
		key = "CHECK " + u
	}
	if r := c.cacheGet(key); r != nil {
		return r
	}
	res := &Response{URL: u, FetchedAt: time.Now()}
	cur := u
	for hop := 0; ; hop++ {
		resp, err := c.once(ctx, method, cur)
		if err != nil {
			res.Err = err.Error()
			res.FinalURL = cur
			break
		}
		if resp.StatusCode == http.StatusTooManyRequests && hop == 0 {
			// one polite retry honoring Retry-After (capped)
			wait := retryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				res.Err = ctx.Err().Error()
				return res
			}
			resp, err = c.once(ctx, method, cur)
			if err != nil {
				res.Err = err.Error()
				res.FinalURL = cur
				break
			}
		}
		if isRedirect(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			res.Chain = append(res.Chain, Hop{URL: cur, Status: resp.StatusCode})
			if loc == "" {
				res.Status = resp.StatusCode
				res.FinalURL = cur
				break
			}
			next, err := resolve(cur, loc)
			if err != nil {
				res.Err = "bad redirect location: " + loc
				res.FinalURL = cur
				break
			}
			if hop+1 >= c.opt.MaxRedirects {
				res.Err = "too many redirects"
				res.FinalURL = next
				break
			}
			cur = next
			continue
		}
		res.Status = resp.StatusCode
		res.FinalURL = cur
		res.ContentType = resp.Header.Get("Content-Type")
		if keepBody && method == http.MethodGet {
			b, err := io.ReadAll(io.LimitReader(resp.Body, c.opt.MaxBody))
			if err != nil {
				res.Err = err.Error()
			}
			res.Body = b
		} else {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		}
		resp.Body.Close()
		break
	}
	if res.Err == "" || res.Status != 0 {
		c.cachePut(key, res)
	}
	return res
}

func (c *Client) once(ctx context.Context, method, u string) (*http.Response, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if err := c.wait(ctx, pu.Host); err != nil {
		return nil, err
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opt.UserAgent)
	req.Header.Set("Accept", "text/html,text/markdown;q=0.9,application/xhtml+xml,*/*;q=0.8")
	return c.http.Do(req)
}

// wait blocks until host may receive another request.
func (c *Client) wait(ctx context.Context, host string) error {
	if c.opt.Delay <= 0 {
		return nil
	}
	c.mu.Lock()
	g := c.hosts[host]
	if g == nil {
		g = &hostGate{}
		c.hosts[host] = g
	}
	c.mu.Unlock()
	g.mu.Lock()
	now := time.Now()
	at := g.next
	if at.Before(now) {
		at = now
	}
	g.next = at.Add(c.opt.Delay)
	g.mu.Unlock()
	if d := time.Until(at); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func isRedirect(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

func resolve(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil {
		d := time.Duration(s) * time.Second
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		return d
	}
	return 2 * time.Second
}

// --- cache ---

func (c *Client) cachePath(key string) string {
	h := sha1.Sum([]byte(key))
	s := hex.EncodeToString(h[:])
	return filepath.Join(c.opt.CacheDir, "http", s[:2], s+".json")
}

func (c *Client) cacheGet(key string) *Response {
	if c.opt.CacheDir == "" || c.opt.CacheTTL <= 0 {
		return nil
	}
	b, err := os.ReadFile(c.cachePath(key))
	if err != nil {
		return nil
	}
	var r Response
	if json.Unmarshal(b, &r) != nil || time.Since(r.FetchedAt) > c.opt.CacheTTL {
		return nil
	}
	return &r
}

func (c *Client) cachePut(key string, r *Response) {
	if c.opt.CacheDir == "" || c.opt.CacheTTL <= 0 {
		return
	}
	// don't cache transient failures
	if r.Status == 429 || r.Status >= 500 || r.Status == 0 {
		return
	}
	p := c.cachePath(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	tmp := p + ".tmp" + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, p)
	}
}

// ErrStatus formats a non-2xx response as an error string.
func ErrStatus(r *Response) error {
	if r.Err != "" {
		return errors.New(r.Err)
	}
	if !r.OK() {
		return fmt.Errorf("HTTP %d", r.Status)
	}
	return nil
}
