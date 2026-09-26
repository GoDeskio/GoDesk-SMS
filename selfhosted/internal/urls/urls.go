package urls

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// NormalizeBaseURL checks a device or webhook URL.
// Paths are allowed only when allowPath is true.
// Link-local and unspecified addresses are rejected. LAN addresses are allowed
// because SMS Gateway local mode is reached on the operator's network.
func NormalizeBaseURL(raw string, allowPath bool) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", errors.New("URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("URL must be http or https")
	}
	if parsed.User != nil {
		return "", errors.New("URL must not include user info")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("URL is missing a host")
	}
	if strings.EqualFold(host, "metadata.google.internal") {
		return "", errors.New("URL host is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return "", errors.New("URL address is not allowed")
		}
	}
	if !allowPath {
		if parsed.Path != "" && parsed.Path != "/" {
			return "", errors.New("device URL must be an origin without a path")
		}
		parsed.Path = ""
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("URL must not include a query or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
