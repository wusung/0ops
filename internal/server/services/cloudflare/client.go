package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.cloudflare.com/client/v4"
	wildcardHostName  = "*.winshare.tw"
)

var (
	// ErrConfigMissing reports missing Cloudflare API configuration.
	ErrConfigMissing = errors.New("cloudflare config missing")
	// ErrRouteMissing reports that the wildcard Cloudflare route is not present.
	ErrRouteMissing = errors.New("cloudflare route missing")
	// ErrRateLimited reports that Cloudflare kept returning 429 after retries.
	ErrRateLimited = errors.New("cloudflare rate limited")
)

// Config holds Cloudflare API credentials and tunnel configuration.
type Config struct {
	// TunnelID is the Cloudflare Tunnel UUID.
	TunnelID string

	// APIToken is the Cloudflare API token with DNS + tunnel permissions.
	APIToken string

	// AccountID is the Cloudflare Account ID.
	AccountID string

	// ZoneID is the Cloudflare Zone ID for the domain (e.g., winshare.tw).
	ZoneID string

	// DisableTunnelIsolation disables all Cloudflare operations.
	DisableTunnelIsolation bool
}

// Client wraps Cloudflare API calls for tunnel and DNS management.
type Client struct {
	config     *Config
	httpClient *http.Client
	baseURL    string
	sleep      func(time.Duration)
}

type apiEnvelope struct {
	Success bool        `json:"success"`
	Errors  []apiError  `json:"errors"`
	Result  []dnsRecord `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

var recordCloudflareMetric = func(string, string) {}

// BindMetrics wires cloudflare operation metrics recorder.
func BindMetrics(recorder func(op, outcome string)) {
	if recorder == nil {
		recordCloudflareMetric = func(string, string) {}
		return
	}
	recordCloudflareMetric = recorder
}

// NewClient creates a new Cloudflare client.
func NewClient(cfg *Config) (*Client, error) {
	return newClient(cfg, defaultAPIBaseURL, &http.Client{Timeout: 15 * time.Second}, time.Sleep)
}

func newClient(cfg *Config, baseURL string, httpClient *http.Client, sleep func(time.Duration)) (*Client, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	return &Client{
		config:     cfg,
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		sleep:      sleep,
	}, nil
}

// RouteAppToDomain validates the shared wildcard route and returns the public hostname.
func (c *Client) RouteAppToDomain(ctx context.Context, _, _, appSlug string) (string, error) {
	subdomain := fmt.Sprintf("%s.winshare.tw", strings.TrimSpace(appSlug))
	if c == nil || c.config == nil || c.config.DisableTunnelIsolation {
		recordCloudflareMetric("dns_create", "success")
		return subdomain, nil
	}
	if err := c.ensureConfigured(); err != nil {
		recordCloudflareMetric("dns_create", metricOutcomeForError(err))
		return "", err
	}
	if err := c.ensureWildcardRoute(ctx); err != nil {
		recordCloudflareMetric("dns_create", metricOutcomeForError(err))
		return "", err
	}
	recordCloudflareMetric("dns_create", "success")
	return subdomain, nil
}

// CreateTunnelRoute validates the shared wildcard route.
func (c *Client) CreateTunnelRoute(ctx context.Context, _, _, _ string) error {
	if c == nil || c.config == nil || c.config.DisableTunnelIsolation {
		recordCloudflareMetric("tunnel_route_create", "success")
		return nil
	}
	if err := c.ensureConfigured(); err != nil {
		recordCloudflareMetric("tunnel_route_create", metricOutcomeForError(err))
		return err
	}
	if err := c.ensureWildcardRoute(ctx); err != nil {
		recordCloudflareMetric("tunnel_route_create", metricOutcomeForError(err))
		return err
	}
	recordCloudflareMetric("tunnel_route_create", "success")
	return nil
}

// DeleteTunnelRoute validates the shared wildcard route still exists.
func (c *Client) DeleteTunnelRoute(ctx context.Context, _ string) error {
	if c == nil || c.config == nil || c.config.DisableTunnelIsolation {
		recordCloudflareMetric("tunnel_route_delete", "success")
		return nil
	}
	if err := c.ensureConfigured(); err != nil {
		recordCloudflareMetric("tunnel_route_delete", metricOutcomeForError(err))
		return err
	}
	if err := c.ensureWildcardRoute(ctx); err != nil {
		recordCloudflareMetric("tunnel_route_delete", metricOutcomeForError(err))
		return err
	}
	recordCloudflareMetric("tunnel_route_delete", "success")
	return nil
}

// GetDomainStatus returns the DNS status for the wildcard route.
func (c *Client) GetDomainStatus(ctx context.Context, hostname string) (map[string]interface{}, error) {
	if c == nil || c.config == nil || c.config.DisableTunnelIsolation {
		recordCloudflareMetric("domain_status", "success")
		return nil, nil
	}
	if err := c.ensureConfigured(); err != nil {
		recordCloudflareMetric("domain_status", metricOutcomeForError(err))
		return nil, err
	}
	target := strings.TrimSpace(hostname)
	if target == "" {
		target = wildcardHostName
	}
	records, err := c.listDNSRecords(ctx, target)
	if err != nil {
		recordCloudflareMetric("domain_status", metricOutcomeForError(err))
		return nil, err
	}
	if len(records) == 0 {
		recordCloudflareMetric("domain_status", metricOutcomeForError(ErrRouteMissing))
		return nil, ErrRouteMissing
	}
	record := records[0]
	recordCloudflareMetric("domain_status", "success")
	return map[string]interface{}{
		"dns_record_id": record.ID,
		"hostname":      record.Name,
		"cname_target":  record.Content,
		"proxied":       record.Proxied,
		"ttl":           record.TTL,
	}, nil
}

func (c *Client) ensureConfigured() error {
	if c.config.TunnelID == "" || c.config.APIToken == "" || c.config.AccountID == "" || c.config.ZoneID == "" {
		return ErrConfigMissing
	}
	return nil
}

func (c *Client) ensureWildcardRoute(ctx context.Context) error {
	records, err := c.listDNSRecords(ctx, wildcardHostName)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return ErrRouteMissing
	}
	for _, record := range records {
		if record.Type != "CNAME" {
			continue
		}
		if strings.Contains(strings.ToLower(record.Content), "cfargotunnel.com") {
			return nil
		}
	}
	return ErrRouteMissing
}

func (c *Client) listDNSRecords(ctx context.Context, name string) ([]dnsRecord, error) {
	query := url.Values{}
	query.Set("type", "CNAME")
	query.Set("name", name)

	body, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records", c.config.ZoneID), query)
	if err != nil {
		return nil, err
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode cloudflare response: %w", err)
	}
	if !envelope.Success {
		if len(envelope.Errors) > 0 {
			switch envelope.Errors[0].Code {
			case 10000, 10001, 10002:
				return nil, ErrConfigMissing
			case 10003:
				return nil, ErrRouteMissing
			}
		}
		return nil, ErrRouteMissing
	}
	return envelope.Result, nil
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	const maxAttempts = 5

	endpoint := c.baseURL + path
	if query != nil && len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.config.APIToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("cloudflare request failed: %w", err)
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read cloudflare response: %w", readErr)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			lastErr = ErrRateLimited
			if attempt == maxAttempts-1 {
				return nil, lastErr
			}
			c.sleep(retryDelay(resp.Header.Get("Retry-After"), attempt))
			continue
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("cloudflare server error: %s", resp.Status)
			if attempt == maxAttempts-1 {
				return nil, lastErr
			}
			c.sleep(retryDelay(resp.Header.Get("Retry-After"), attempt))
			continue
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, ErrConfigMissing
		case resp.StatusCode == http.StatusNotFound:
			return nil, ErrRouteMissing
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("cloudflare request failed: %s", resp.Status)
		}

		return body, nil
	}

	return nil, lastErr
}

func retryDelay(retryAfter string, attempt int) time.Duration {
	if retryAfter != "" {
		if seconds, err := time.ParseDuration(retryAfter + "s"); err == nil {
			return seconds
		}
		if parsed, err := time.ParseDuration(retryAfter); err == nil {
			return parsed
		}
	}

	delay := time.Second << attempt
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func metricOutcomeForError(err error) string {
	switch {
	case errors.Is(err, ErrRateLimited):
		return "throttled"
	case err == nil:
		return "success"
	default:
		return "error"
	}
}
