package qemurun

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

var ErrInvalidRPCEndpoint = errors.New("qemu runner: invalid RPC endpoint")

func NetworkListenAddress(transport, endpoint string) (network string, address string, err error) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "ipc":
		if !filepath.IsAbs(endpoint) {
			return "", "", fmt.Errorf("%w: IPC path must be absolute", ErrInvalidRPCEndpoint)
		}
		return "unix", filepath.Clean(endpoint), nil
	case "tcp":
		if _, _, err := net.SplitHostPort(endpoint); err != nil {
			return "", "", fmt.Errorf("%w: TCP address %q: %v", ErrInvalidRPCEndpoint, endpoint, err)
		}
		return "tcp", endpoint, nil
	case "http", "ws":
		parsed, err := parseTransportURL(transport, endpoint)
		if err != nil {
			return "", "", err
		}
		return "tcp", urlListenAddress(parsed), nil
	default:
		return "", "", fmt.Errorf("%w: unsupported transport %q", ErrInvalidRPCEndpoint, transport)
	}
}

func PrepareGuestEndpoint(transport, endpoint, workRoot string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "ipc":
		if !filepath.IsAbs(workRoot) {
			return "", fmt.Errorf("%w: guest work root must be absolute", ErrInvalidRPCEndpoint)
		}
		return filepath.Join(filepath.Clean(workRoot), "evaluator.sock"), nil
	case "tcp":
		_, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return "", fmt.Errorf("%w: TCP address %q: %v", ErrInvalidRPCEndpoint, endpoint, err)
		}
		return net.JoinHostPort("127.0.0.1", port), nil
	case "http", "ws":
		parsed, err := parseTransportURL(transport, endpoint)
		if err != nil {
			return "", err
		}
		port := parsed.Port()
		if port == "" {
			port = defaultURLPort(parsed.Scheme)
		}
		parsed.Host = net.JoinHostPort("127.0.0.1", port)
		return parsed.String(), nil
	default:
		return "", fmt.Errorf("%w: unsupported transport %q", ErrInvalidRPCEndpoint, transport)
	}
}

func parseTransportURL(transport, endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%w: malformed %s URL %q", ErrInvalidRPCEndpoint, transport, endpoint)
	}
	allowed := false
	switch strings.ToLower(transport) {
	case "http":
		allowed = parsed.Scheme == "http" || parsed.Scheme == "https"
	case "ws":
		allowed = parsed.Scheme == "ws" || parsed.Scheme == "wss"
	}
	if !allowed {
		return nil, fmt.Errorf("%w: scheme %q does not match transport %q", ErrInvalidRPCEndpoint, parsed.Scheme, transport)
	}
	if parsed.Port() != "" {
		if _, _, err := net.SplitHostPort(parsed.Host); err != nil {
			return nil, fmt.Errorf("%w: malformed URL host %q", ErrInvalidRPCEndpoint, parsed.Host)
		}
	}
	return parsed, nil
}

func urlListenAddress(parsed *url.URL) string {
	port := parsed.Port()
	if port == "" {
		port = defaultURLPort(parsed.Scheme)
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}

func defaultURLPort(scheme string) string {
	switch scheme {
	case "https", "wss":
		return "443"
	default:
		return "80"
	}
}
