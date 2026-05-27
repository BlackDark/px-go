package network

import (
	"context"
	"net/netip"
	"testing"
)

func TestMatchIPPatterns(t *testing.T) {
	matcher, err := Parse("127.0.0.1,192.168.1.*,10.0.0.1-10.0.0.9,172.16.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"192.168.1.55", true},
		{"10.0.0.8", true},
		{"172.16.2.1", true},
		{"8.8.8.8", false},
	} {
		if got := matcher.MatchIP(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Fatalf("MatchIP(%s)=%v want %v", tc.ip, got, tc.want)
		}
	}
}

func TestMatchHostDomains(t *testing.T) {
	matcher, err := Parse("example.com,<local>")
	if err != nil {
		t.Fatal(err)
	}
	resolver := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "localhost" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return nil, nil
	}
	if !matcher.MatchHost(context.Background(), "api.example.com", resolver) {
		t.Fatal("expected subdomain match")
	}
	if !matcher.MatchHost(context.Background(), "localhost", resolver) {
		t.Fatal("expected local match")
	}
	if matcher.MatchHost(context.Background(), "google.com", resolver) {
		t.Fatal("unexpected match")
	}
}
