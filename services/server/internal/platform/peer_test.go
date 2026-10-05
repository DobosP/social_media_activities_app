package platform

import "testing"

func TestPeerKey(t *testing.T) {
	for _, test := range []struct{ address, want string }{
		{"192.0.2.9:1000", "192.0.2.9"},
		{"192.0.2.9", "192.0.2.9"},
		{" 192.0.2.9 ", "192.0.2.9"},
		{"[::ffff:192.0.2.9]:3000", "192.0.2.9"},
		{"[2001:db8:1:2::1]:443", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff:ffff:ffff:fffe]:80", "2001:db8:1:2::/64"},
		{"2001:db8:1:2::7", "2001:db8:1:2::/64"},
		{"[2001:db8:1:3::1]:443", "2001:db8:1:3::/64"},
		{"[fe80::1%eth0]:80", "fe80::/64"},
		{"", "unknown"},
		{"not-an-address", "unknown"},
		{"[garbage]:80", "unknown"},
	} {
		if got := PeerKey(test.address); got != test.want {
			t.Fatalf("PeerKey(%q) = %q, want %q", test.address, got, test.want)
		}
	}
	if PeerKey("[2001:db8:1:2::1]:1") != PeerKey("[2001:db8:1:2:abcd::9]:2") {
		t.Fatal("two addresses in one /64 split")
	}
	if PeerKey("[2001:db8:1:2::1]:1") == PeerKey("[2001:db8:1:3::1]:1") {
		t.Fatal("different /64 prefixes merged")
	}
}
