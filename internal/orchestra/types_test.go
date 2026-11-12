package orchestra

import (
	"errors"
	"testing"
)

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		in      string
		want    Strategy
		wantErr bool
	}{
		{"auto", StrategyAuto, false},
		{"AUTO", StrategyAuto, false},
		{"clear", StrategyAuto, false},
		{"", StrategyAuto, false},
		{"skip", StrategyOff, false},
		{"0", StrategyOff, false},
		{"7", 7, false},
		{" 12 ", 12, false},
		{"043", 43, false},
		{"-1", 0, true},
		{"1.5", 0, true},
		{"abc", 0, true},
		{"1e1", 0, true},
		{"99999999999999999999", 0, true},
	}
	for _, tc := range tests {
		got, err := ParseStrategy(tc.in)
		if tc.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("ParseStrategy(%q) err = %v, want ErrInvalid", tc.in, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseStrategy(%q) = (%v,%v), want %v", tc.in, got, err, tc.want)
		}
	}
}

func TestStrategyString(t *testing.T) {
	tests := []struct {
		s    Strategy
		want string
	}{{StrategyAuto, "auto"}, {StrategyOff, "0"}, {5, "5"}, {43, "43"}}
	for _, tc := range tests {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("Strategy(%d).String() = %q, want %q", int(tc.s), got, tc.want)
		}
	}
	if !StrategyAuto.IsAuto() || StrategyOff.IsAuto() || !StrategyOff.IsOff() || Strategy(3).IsOff() {
		t.Error("IsAuto/IsOff wrong")
	}
}

func TestParseProto(t *testing.T) {
	tests := []struct {
		in      string
		want    Proto
		wantErr bool
	}{
		{"tls", ProtoTLS, false},
		{"HTTP", ProtoHTTP, false},
		{" udp ", ProtoUDP, false},
		{"quic", "", true},
		{"", "", true},
	}
	for _, tc := range tests {
		got, err := ParseProto(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseProto(%q) = (%q,%v)", tc.in, got, err)
		}
	}
}

func TestScopeHelpers(t *testing.T) {
	valid := []string{"default", "mark:1", "mark:255", "mark:1000"}
	invalid := []string{"", "mark:0", "mark:", "mark:-1", "mark:01", "mark:x", "Default", "other", "mark:1\t", "mark:1 "}
	for _, s := range valid {
		if !ValidScope(s) {
			t.Errorf("ValidScope(%q) = false", s)
		}
	}
	for _, s := range invalid {
		if ValidScope(s) {
			t.Errorf("ValidScope(%q) = true", s)
		}
	}
	if n, ok := MarkOf("mark:42"); !ok || n != 42 {
		t.Errorf("MarkOf = (%d,%v)", n, ok)
	}
	if _, ok := MarkOf("default"); ok {
		t.Error("MarkOf(default) must fail")
	}
	if MarkScope(7) != "mark:7" {
		t.Error("MarkScope")
	}
	if sc, err := normScope(" MARK:3 "); err != nil || sc != "mark:3" {
		t.Errorf("normScope = (%q,%v)", sc, err)
	}
	if sc, err := normScope(""); err != nil || sc != ScopeDefault {
		t.Errorf("normScope(empty) = (%q,%v)", sc, err)
	}
	if !IsCustomDomain("example.com") || !IsCustomDomain("a-b.c.org") || IsCustomDomain("3") || IsCustomDomain("localhost") || IsCustomDomain("-a.com") {
		t.Error("IsCustomDomain wrong")
	}
}

func TestParseLockLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantRec  Record
		wantCols int
		wantOK   bool
	}{
		{"3 cols", "3\ttls\t7", Record{ScopeDefault, "3", ProtoTLS, 7}, 3, true},
		{"2 cols means tls", "3\t7", Record{ScopeDefault, "3", ProtoTLS, 7}, 2, true},
		{"4 cols default", "default\t5\tudp\t2", Record{ScopeDefault, "5", ProtoUDP, 2}, 4, true},
		{"4 cols mark", "mark:2\t1\thttp\t4", Record{"mark:2", "1", ProtoHTTP, 4}, 4, true},
		{"zero strategy", "6\tudp\t0", Record{ScopeDefault, "6", ProtoUDP, 0}, 3, true},
		{"crlf", "3\ttls\t7\r", Record{ScopeDefault, "3", ProtoTLS, 7}, 3, true},
		{"spaces trimmed", " 3 \t TLS \t 7 ", Record{ScopeDefault, "3", ProtoTLS, 7}, 3, true},
		{"uppercase scope/profile lowered", "MARK:2\tExample.COM\ttls\t9", Record{"mark:2", "example.com", ProtoTLS, 9}, 4, true},
		{"comment", "# note", Record{}, 0, false},
		{"blank", "", Record{}, 0, false},
		{"one col", "3", Record{}, 0, false},
		{"five cols", "a\tb\tc\td\te", Record{}, 0, false},
		{"bad proto", "3\tquic\t1", Record{}, 0, false},
		{"bad strategy", "3\ttls\tauto", Record{}, 0, false},
		{"negative strategy", "3\ttls\t-1", Record{}, 0, false},
		{"empty profile", "\ttls\t1", Record{}, 0, false},
		{"empty scope in 4 cols", "\t3\ttls\t1", Record{}, 0, false},
		{"empty strategy", "3\ttls\t", Record{}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, cols, ok := parseLockLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if rec != tc.wantRec || cols != tc.wantCols {
				t.Errorf("got (%v,%d), want (%v,%d)", rec, cols, tc.wantRec, tc.wantCols)
			}
		})
	}
}

func TestParseScopedLine(t *testing.T) {
	rec, cols, ok := parseScopedLine("mark:2", "3\ttls\t7")
	if !ok || cols != 3 || rec != (Record{"mark:2", "3", ProtoTLS, 7}) {
		t.Errorf("3 cols: (%v,%d,%v)", rec, cols, ok)
	}
	for _, line := range []string{"3\t7", "default\t3\ttls\t7", "", "# c"} {
		if _, _, ok := parseScopedLine("mark:2", line); ok {
			t.Errorf("parseScopedLine(%q) must fail", line)
		}
	}
}
