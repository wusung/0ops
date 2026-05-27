package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type tunnelConnectorsEnvelope struct {
	Success bool       `json:"success"`
	Errors  []apiError `json:"errors"`
	Result  []struct {
		ID string `json:"id"`
	} `json:"result"`
}

// GetTunnelConnectorsReady returns the number of currently-active cloudflared
// connectors registered against the configured tunnel.
//
// The metric source for the `TunnelConnectorsLow` / `TunnelDown` alerts
// defined in `slo-and-alerting` § 6.4 is the gauge
// `zeroops_cloudflare_tunnel_connectors_ready`; the reconciler is expected to
// call this method on a fixed cadence and feed the result through
// `observability.Metrics.SetCloudflareTunnelConnectorsReady`.
func (c *Client) GetTunnelConnectorsReady(ctx context.Context) (int, error) {
	if c == nil || c.config == nil || c.config.DisableTunnelIsolation {
		recordCloudflareMetric("tunnel_status", "success")
		return 0, nil
	}
	if err := c.ensureConfigured(); err != nil {
		recordCloudflareMetric("tunnel_status", metricOutcomeForError(err))
		return 0, err
	}
	body, err := c.request(ctx, "tunnel_status", http.MethodGet,
		fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/connections", c.config.AccountID, c.config.TunnelID), nil)
	if err != nil {
		recordCloudflareMetric("tunnel_status", metricOutcomeForError(err))
		return 0, err
	}
	var envelope tunnelConnectorsEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		recordCloudflareMetric("tunnel_status", "error")
		return 0, fmt.Errorf("decode cloudflare tunnel connections: %w", err)
	}
	if !envelope.Success {
		recordCloudflareMetric("tunnel_status", "error")
		return 0, errors.New("cloudflare tunnel status request failed")
	}
	recordCloudflareMetric("tunnel_status", "success")
	return len(envelope.Result), nil
}
