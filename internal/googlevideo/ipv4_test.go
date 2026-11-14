package googlevideo

import "testing"

func TestParseIPv4(t *testing.T) {
	good := []string{"0.0.0.0", "1.2.3.4", "255.255.255.255", "8.8.8.8", "100.200.10.1"}
	for _, s := range good {
		if _, ok := ParseIPv4(s); !ok {
			t.Errorf("ParseIPv4(%q) must be valid", s)
		}
	}
	o, _ := ParseIPv4("192.0.2.17")
	if o != [4]int{192, 0, 2, 17} {
		t.Errorf("octets = %v", o)
	}
	bad := []string{
		"", "1.2.3", "1.2.3.4.5", "256.1.1.1", "1.2.3.256", "01.2.3.4", "1.02.3.4", "1.2.3.04",
		".1.2.3", "1.2.3.", "1..2.3", " 1.2.3.4", "1.2.3.4 ", "1.2.3.4\n", "a.b.c.d", "1.2.3.-4",
		"1.2.3.4/24", "::1", "1.2.3.0004", "1,2,3,4", "+1.2.3.4",
	}
	for _, s := range bad {
		if _, ok := ParseIPv4(s); ok {
			t.Errorf("ParseIPv4(%q) must be rejected", s)
		}
	}
}

func TestPublicIPv4(t *testing.T) {
	public := []string{
		"8.8.8.8", "1.1.1.1", "9.9.9.9", "142.250.74.14", "11.0.0.1", "172.15.255.255", "172.32.0.1",
		"192.0.3.1", "192.167.1.1", "192.169.1.1", "198.17.255.255", "198.20.0.1", "198.51.101.1",
		"203.0.112.1", "203.0.114.1", "100.63.255.255", "100.128.0.1", "223.255.255.255", "169.253.1.1",
		"192.88.98.1", "192.88.100.1", "192.31.195.1", "192.52.192.1", "192.175.47.1",
	}
	for _, ip := range public {
		if !PublicIPv4(ip) {
			t.Errorf("%s must be public", ip)
		}
	}
	reserved := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "10.255.255.255", "127.0.0.1", "127.255.255.254",
		"224.0.0.1", "239.255.255.255", "240.0.0.1", "255.255.255.255", "169.254.1.1",
		"172.16.0.1", "172.31.255.255", "192.168.1.1", "192.168.255.255",
		"192.0.0.1", "192.0.1.1", "192.0.2.1", "192.88.99.1", "192.31.196.5", "192.52.193.5", "192.175.48.5",
		"198.18.0.1", "198.19.255.255", "198.51.100.7", "203.0.113.9",
		"100.64.0.1", "100.127.255.255",
		// не IPv4
		"", "google.com", "1.2.3", "01.2.3.4", "8.8.8.8 ", "256.1.1.1",
	}
	for _, ip := range reserved {
		if PublicIPv4(ip) {
			t.Errorf("%q must not be public", ip)
		}
	}
}

func TestNormalizeHostname(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"rr1---sn-h5gtov-n8vl.googlevideo.com", "rr1---sn-h5gtov-n8vl.googlevideo.com", true},
		{"rr18---sn-pmcg-bg0s.googlevideo.com", "rr18---sn-pmcg-bg0s.googlevideo.com", true},
		{"RR2---SN-4G5EDNLY.GoogleVideo.COM", "rr2---sn-4g5ednly.googlevideo.com", true},
		{"rr2---sn-4g5ednly.googlevideo.com.", "rr2---sn-4g5ednly.googlevideo.com", true},
		{"rr2.googlevideo.com.", "", false},          // не media RR
		{"a.b.googlevideo.com", "", false},           // не media RR
		{"redirector.googlevideo.com", "", false},
		{"sn-pmcg-bg0s.googlevideo.com", "", false}, // без rrN---
		{"googlevideo.com", "", false},
		{"googlevideo.com.", "", false},
		{".googlevideo.com", "", false},
		{"a..googlevideo.com", "", false},
		{"evilgooglevideo.com", "", false},
		{"rr1.googlevideo.com.evil.org", "", false},
		{"youtube.com", "", false},
		{"", "", false},
		{"rr_1---sn-abc.googlevideo.com", "", false},
		{"rr1---sn-abc.googlevideo.com/x", "", false},
		{"rr1---sn-abc.googlevideo.cоm", "", false}, // кириллическая «о»
	}
	for _, tc := range cases {
		got, ok := NormalizeHostname(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("NormalizeHostname(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
		if ValidHostname(tc.in) != tc.ok {
			t.Errorf("ValidHostname(%q) mismatch", tc.in)
		}
	}
}
