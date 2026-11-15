package netcheck

import "testing"

func TestClassifyMediaRange(t *testing.T) {
	tests := []struct {
		name   string
		dns    bool
		tls    bool
		hasURL bool
		d16    *Download
		d64    *Download
		want   string
	}{
		{"dns", true, false, false, nil, nil, MediaDNS},
		{"tls only", false, true, false, nil, nil, MediaTLS},
		{"no tls", false, false, false, nil, nil, MediaNone},
		{"tiny", false, true, true, &Download{Size: 300, State: DLOK}, &Download{Size: 300, State: DLOK}, MediaTiny},
		{"midstream", false, true, true, &Download{Size: 16384, State: DLOK}, &Download{Size: 8000, State: DLCut}, MediaMidstream},
		{"play", false, true, true, &Download{Size: 16384, State: DLOK}, &Download{Size: 65536, State: DLOK}, MediaPlay},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyMediaRange(tc.dns, tc.tls, tc.hasURL, tc.d16, tc.d64)
			if got.Class != tc.want {
				t.Fatalf("class=%q want %q text=%q", got.Class, tc.want, got.Text)
			}
		})
	}
}

func TestLooksLikeGooglevideoMediaURL(t *testing.T) {
	ok := []string{
		"https://rr1---sn-abc.googlevideo.com/videoplayback?sig=abc&expire=1999999999",
	}
	bad := []string{"", "http://x", "https://www.youtube.com/", "ftp://gv", "https://fakegooglevideo.com/videoplayback?sig=x", "https://rr1.googlevideo.com/videoplayback?id=1"}
	for _, u := range ok {
		if !looksLikeGooglevideoMediaURL(u) {
			t.Errorf("want ok: %s", u)
		}
	}
	for _, u := range bad {
		if looksLikeGooglevideoMediaURL(u) {
			t.Errorf("want bad: %s", u)
		}
	}
}

func TestApplyMediaToResultMidstream(t *testing.T) {
	r := TargetResult{Verdict: VerdictOK, Text: "TLS ok"}
	applyMediaToResult(&r, MediaProbe{Class: MediaMidstream, Text: "cut"})
	if r.Verdict != VerdictFail || r.Media == nil || r.Media.Class != MediaMidstream {
		t.Fatalf("%+v", r)
	}
}

func TestApplyMediaUnknownDoesNotReportTLSAsMediaSuccess(t *testing.T) {
	r := TargetResult{Verdict: VerdictOK, Text: "TLS CDN отвечает"}
	applyMediaToResult(&r, MediaProbe{Class: MediaUnknown, Text: "Медиаданные не получены"})
	if r.Verdict != VerdictUnknown || r.Text != "Медиаданные не получены" {
		t.Fatalf("unknown media result = %+v", r)
	}
}
