package netsafe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// ErrPrivateAddressBlocked marks a failed dial where the guard refused at least one address.
var ErrPrivateAddressBlocked = errors.New("blocked private/internal address")

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("fec0::/10"),
}

var nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")

// IsBlockedIP reports whether ip is a private, loopback, or internal address.
func IsBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	if nat64WellKnown.Contains(addr) {
		embedded := addr.As16()
		return IsBlockedIP(net.IP(embedded[12:16]))
	}
	return false
}

type allowPrivateCtxKey struct{}

// ContextWithAllowPrivate returns a context with allowPrivate flag.
func ContextWithAllowPrivate(ctx context.Context, allow bool) context.Context {
	return context.WithValue(ctx, allowPrivateCtxKey{}, allow)
}

// AllowPrivateFromContext checks if private addresses are allowed in context.
func AllowPrivateFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(allowPrivateCtxKey{}).(bool)
	return v
}

var defaultDialer = &net.Dialer{Timeout: 10 * time.Second}

const minDialAttempt = 2 * time.Second

// SSRFGuardedDialContext dials with SSRF protections.
func SSRFGuardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultDialer.Timeout)
	defer cancel()
	allowPrivate := AllowPrivateFromContext(ctx)
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		ips, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
	}
	var lastErr, blockedErr error
	for i, ipAddr := range ips {
		if !allowPrivate && IsBlockedIP(ipAddr.IP) {
			blockedErr = fmt.Errorf("%w %s", ErrPrivateAddressBlocked, ipAddr.IP)
			continue
		}
		deadline, _ := ctx.Deadline()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		attempt := max(remaining/time.Duration(len(ips)-i), min(minDialAttempt, remaining))
		attemptCtx, attemptCancel := context.WithTimeout(ctx, attempt)
		conn, derr := defaultDialer.DialContext(attemptCtx, network, net.JoinHostPort(ipAddr.IP.String(), port))
		attemptCancel()
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if blockedErr != nil {
		if lastErr != nil {
			return nil, fmt.Errorf("%w; %w", blockedErr, lastErr)
		}
		return nil, blockedErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable address for %s", host)
	}
	return nil, lastErr
}

var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// NormalizeHost validates and returns the cleaned host.
func NormalizeHost(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("address is required")
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = addr[1 : len(addr)-1]
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String(), nil
	}
	if len(addr) > 253 || !hostnamePattern.MatchString(addr) {
		return "", fmt.Errorf("invalid host %q", addr)
	}
	return addr, nil
}
