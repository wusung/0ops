package notify

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"
)

// errBlockedDeliveryIP / errRedirectBlocked are returned by the delivery client
// when a connection would reach a non-public IP or follow a redirect. They
// surface as transport errors → the delivery is treated as failed and retried.
var (
	errBlockedDeliveryIP = errors.New("notify: delivery target resolves to a non-public ip")
	errRedirectBlocked   = errors.New("notify: delivery target attempted a redirect")
)

// safeDeliveryClient is the production delivery HTTP client. It closes the
// delivery-time SSRF gap that config-time URL validation cannot (DNS rebinding,
// redirect-follow, spec § 6.4): the dialer's Control hook re-checks the ACTUAL
// resolved IP right before connect and rejects any non-public address, and
// redirects are refused outright (a webhook receiver must not redirect). Connect
// timeout 5s, overall 10s (spec § 6.4).
func safeDeliveryClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: dialGuard}
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errRedirectBlocked
		},
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			DisableKeepAlives:     true,
		},
	}
}

// dialGuard runs after DNS resolution with the concrete ip:port about to be
// dialed; rejecting non-public targets here defeats DNS rebinding (the address
// is the post-resolution IP, not the hostname).
func dialGuard(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !isPublicIP(ip) {
		return errBlockedDeliveryIP
	}
	return nil
}
