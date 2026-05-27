package network

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type Resolver func(context.Context, string) ([]netip.Addr, error)

type ipRange struct {
	start netip.Addr
	end   netip.Addr
}

type Matcher struct {
	all      bool
	ranges   []ipRange
	prefixes []netip.Prefix
	domains  []string
	hosts    map[string]struct{}
}

func Parse(value string) (*Matcher, error) {
	m := &Matcher{hosts: map[string]struct{}{}}
	for _, item := range splitCSV(value) {
		switch {
		case item == "*" || item == "*.*.*.*":
			m.all = true
		case strings.EqualFold(item, "<local>"):
			m.domains = append(m.domains, "localhost")
			m.prefixes = append(m.prefixes, netip.MustParsePrefix("127.0.0.0/8"))
		case strings.Contains(item, "/"):
			prefix, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, err
			}
			m.prefixes = append(m.prefixes, prefix.Masked())
		case strings.Count(item, "-") == 1:
			parts := strings.SplitN(item, "-", 2)
			start, err := netip.ParseAddr(strings.TrimSpace(parts[0]))
			if err != nil {
				return nil, err
			}
			end, err := netip.ParseAddr(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			m.ranges = append(m.ranges, ipRange{start: start.Unmap(), end: end.Unmap()})
		case strings.Contains(item, "*"):
			rng, err := wildcardRange(item)
			if err != nil {
				return nil, err
			}
			m.ranges = append(m.ranges, rng)
		case isIP(item):
			addr, _ := netip.ParseAddr(item)
			m.prefixes = append(m.prefixes, netip.PrefixFrom(addr.Unmap(), addr.BitLen()))
		default:
			item = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(item), "."))
			m.domains = append(m.domains, item)
			m.hosts[item] = struct{}{}
		}
	}
	return m, nil
}

func (m *Matcher) MatchIP(ip netip.Addr) bool {
	if m == nil {
		return false
	}
	if m.all {
		return true
	}
	ip = ip.Unmap()
	for _, prefix := range m.prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	for _, rng := range m.ranges {
		if compareAddr(ip, rng.start) >= 0 && compareAddr(ip, rng.end) <= 0 {
			return true
		}
	}
	return false
}

func (m *Matcher) MatchHost(ctx context.Context, host string, resolver Resolver) bool {
	if m == nil {
		return false
	}
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return m.MatchIP(ip)
	}
	if _, ok := m.hosts[host]; ok {
		return true
	}
	for _, domain := range m.domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	if resolver == nil {
		resolver = DefaultResolver
	}
	ips, err := resolver(ctx, host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if m.MatchIP(ip) {
			return true
		}
	}
	return false
}

func DefaultResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, ip := range addrs {
		out = append(out, ip.Unmap())
	}
	return out, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if strings.Contains(host, ":") {
		if h, _, err := net.SplitHostPort(host); err == nil {
			return strings.Trim(h, "[]")
		}
	}
	return strings.Trim(host, "[]")
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func wildcardRange(pattern string) (ipRange, error) {
	parts := strings.Split(pattern, ".")
	if len(parts) != 4 {
		return ipRange{}, fmt.Errorf("invalid wildcard %q", pattern)
	}
	startParts := make([]string, 4)
	endParts := make([]string, 4)
	for i, part := range parts {
		switch part {
		case "*":
			startParts[i] = "0"
			endParts[i] = "255"
		default:
			if _, err := strconv.Atoi(part); err != nil {
				return ipRange{}, fmt.Errorf("invalid wildcard %q", pattern)
			}
			startParts[i] = part
			endParts[i] = part
		}
	}
	start := netip.MustParseAddr(strings.Join(startParts, "."))
	end := netip.MustParseAddr(strings.Join(endParts, "."))
	return ipRange{start: start, end: end}, nil
}

func compareAddr(a, b netip.Addr) int {
	ab := a.As4()
	bb := b.As4()
	for i := range ab {
		if ab[i] < bb[i] {
			return -1
		}
		if ab[i] > bb[i] {
			return 1
		}
	}
	return 0
}

func isIP(value string) bool {
	_, err := netip.ParseAddr(value)
	return err == nil
}
