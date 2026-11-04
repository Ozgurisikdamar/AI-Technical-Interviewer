package api

import (
	"fmt"
	"net/netip"
	"testing"
	"time"
)

// One IPv6 subscriber controls a whole /64: rotating addresses inside it must not reset the limit.
func TestLimiterKeysIPv6ByPrefixAndIPv4ByAddress(t *testing.T) {
	l := newIPLimiter(2)
	now := time.Now()
	for i := range 2 {
		if !l.allow(netip.MustParseAddr(fmt.Sprintf("2001:db8:1:2::%x", i+1)), now) {
			t.Fatalf("request %d within the limit was refused", i)
		}
	}
	if l.allow(netip.MustParseAddr("2001:db8:1:2:ffff::1"), now) {
		t.Fatal("another address in the same /64 shares the limit")
	}
	if !l.allow(netip.MustParseAddr("2001:db8:1:3::1"), now) {
		t.Fatal("another /64 has its own limit")
	}
	for _, a := range []string{"192.0.2.1", "192.0.2.2", "::ffff:192.0.2.3"} {
		if !l.allow(netip.MustParseAddr(a), now) {
			t.Fatalf("IPv4 %s is limited per address", a)
		}
	}
}

// Distinct keys cannot grow the table without bound; the least recently seen are evicted.
func TestLimiterTableIsBounded(t *testing.T) {
	l := newIPLimiter(5)
	l.max = 100
	now := time.Now()
	hot := netip.MustParseAddr("198.51.100.7")
	for i := range 1000 {
		l.allow(hot, now) // seen constantly: never the least recent
		l.allow(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}), now)
	}
	if len(l.entries) > 100 || l.order.Len() > 100 {
		t.Fatalf("table grew to %d", len(l.entries))
	}
	if _, ok := l.entries[hot]; !ok {
		t.Fatal("a recently seen client was evicted")
	}
}
