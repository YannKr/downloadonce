// Package safehttp makes outbound HTTP requests to user-supplied URLs without
// letting the caller reach the machine the server runs on or the network it
// sits in. Without it a webhook URL or an asset import URL is a request
// forgery primitive: http://169.254.169.254/ hands over cloud instance
// credentials, and http://127.0.0.1/ reaches anything else on the host.
package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// blocked reports whether addr is one a user-supplied URL must never reach.
func blocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return true
	}
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast()
}

// ValidateURL rejects a URL that is not http(s), carries credentials, or names
// a host that resolves to a private, loopback or link-local address. It is a
// check at entry; Client enforces the same rule again per connection, which is
// what stops a DNS answer that changes between the two.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL must start with http:// or https://")
	}
	if u.User != nil {
		return errors.New("URL must not contain credentials")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL must have a host")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if blocked(addr) {
			return fmt.Errorf("%s is not a public address", addr)
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve %s", host)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || blocked(addr) {
			return fmt.Errorf("%s resolves to an address that is not public", host)
		}
	}
	return nil
}

// Client returns an HTTP client that refuses to connect to a private,
// loopback or link-local address. The check runs in the dialer's Control hook,
// so it sees the address actually being connected to, after DNS and on every
// redirect hop.
func Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("cannot parse destination address %q", host)
			}
			if blocked(addr) {
				return fmt.Errorf("destination %s is not a public address", addr)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}
