package mask_test

import (
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/mask"
)

// Fixtures are obviously fake and stay below the repository's secret-signature thresholds.
const (
	fakeHash  = "0123456789abcdef0123456789abcdef"
	fakePEM   = "-----BEGIN TEST KEY-----\nZmFrZS1wZW0tYm9keQ==\nc2Vjb25kLWxpbmU=\n-----END TEST KEY-----"
	fakeAge   = "AGE-SECRET-KEY-1FAKEEXAMPLE"
	fakeTSKey = "tskey-auth-kEXAMPLE-fakevalue"
)

func TestMaskReplacesEachPattern(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"tailscale key", "join with " + fakeTSKey + " now", "join with [masked] now"},
		{"tailscale api key", "key=tskey-api-kEXAMPLE.", "key=[masked]."},
		{"age identity", "identity " + fakeAge + " loaded", "identity [masked] loaded"},
		{"age identity lower case", "id age-secret-key-1fakeexample ok", "id [masked] ok"},
		{"token yaml", "rke2_token: abc123 # set", "rke2_token: [masked] # set"},
		{"token flag", "run --token=EXAMPLE --debug", "run --token=[masked] --debug"},
		{"token json", `{"api_token": "a b\"c", "user": "ops"}`, `{"api_token": "[masked]", "user": "ops"}`},
		{"password yaml", "bootstrapPassword: hunter2\nnext: 1", "bootstrapPassword: [masked]\nnext: 1"},
		{"password single quoted", "password: 'p w'", "password: '[masked]'"},
		{"password upper case", "PASSWORD=hunter2", "PASSWORD=[masked]"},
		{"pem block", "before\n" + fakePEM + "\nafter", "before\n[masked]\nafter"},
		{"pem without end", "key: -----BEGIN TEST KEY-----\nZmFrZQ==", "key: [masked]"},
		{"rke2 token", "server K10" + fakeHash + " joined", "server [masked] joined"},
		{"rke2 full token", "use K10" + fakeHash + "::server:fakepass end", "use [masked] end"},
		{"bearer header", "Authorization: Bearer eyJfake.payload.sig rest", "Authorization: Bearer [masked] rest"},
		{"bearer lower case", "authorization: bearer token-abc:def", "authorization: bearer [masked]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mask.Mask(tt.in); got != tt.want {
				t.Fatalf("Mask(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaskKeepsTextWithoutSecrets(t *testing.T) {
	tests := []string{
		"",
		"install acme finished: changed=3 failed=0",
		"token expired, run ktags secret rotate acme rke2_token",
		"password prompt shown to the operator",
		"Enter password:",
		"password: ",
		"token:\n  next line",
		"the tskey prefix marks Tailscale keys",
		"tskey-",
		"AGE-SECRET-KEY-",
		"BEGIN CERTIFICATE without dashes",
		"----BEGIN four dashes only",
		"K10 is not a token; neither is K10abc",
		"a bearer",
		"Bearer\n",
		"node acme-srv-1 (203.0.113.11:22) ready",
		"ssh_port: 22",
		"ünïcödé ✓ text",
	}
	for _, in := range tests {
		if got := mask.Mask(in); got != in {
			t.Errorf("Mask(%q) = %q, want unchanged", in, got)
		}
	}
}

// Overlapping matches from different patterns must all be masked, whatever the order.
func TestMaskOverlappingPatterns(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"key named like a tailscale key", "tskey-password: hunter2", "[masked]: [masked]"},
		{"bearer as a token value", "token: Bearer abc", "token: [masked] [masked]"},
		{"pair as a token value", "token: password: hunter2", "token: [masked] [masked]"},
		{"rke2 token as a password value", "password=K10" + fakeHash, "password=[masked]"},
		{"secret inside pem", "-----BEGIN X-----\ntoken: abc\n-----END X----- tail", "[masked] tail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mask.Mask(tt.in); got != tt.want {
				t.Fatalf("Mask(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// secretBodies are the parts of each fixture that must never survive masking.
var secretBodies = map[string]string{
	fakeTSKey:                           "kEXAMPLE-fakevalue",
	fakeAge:                             "1FAKEEXAMPLE",
	"password: hunter2":                 "hunter2",
	"token=abc123":                      "abc123",
	fakePEM:                             "ZmFrZS1wZW0tYm9keQ==",
	"K10" + fakeHash + "::server:pw123": fakeHash,
	"Bearer eyJfake.sig":                "eyJfake.sig",
}

// FuzzMask runs its seed corpus in every `go test`; `go test -fuzz FuzzMask ./internal/mask`
// explores further. Properties: no panic on arbitrary input, and a secret between arbitrary text
// is never visible in the output. Mask is not strictly idempotent: when ranges from two patterns
// overlap, a second pass may mask a trailing fragment the first left, which hides more, never less.
func FuzzMask(f *testing.F) {
	for secret := range secretBodies {
		f.Add(secret, "", "")
		f.Add("prefix ", secret, " suffix")
		f.Add("tskey-", secret, "\"")
		f.Add("token: ", secret, "'")
	}
	f.Add("\xff\xfe-----BEGIN", "\x00", "Bearer")
	f.Add("password: \"unterminated", "\\", "")

	f.Fuzz(func(t *testing.T, a, b, c string) {
		mask.Mask(a + b + c)
		for secret, body := range secretBodies {
			if strings.Contains(a, body) || strings.Contains(c, body) {
				continue // the fuzzer wrote the body into the surrounding text itself
			}
			out := mask.Mask(a + " " + secret + " " + c)
			if strings.Contains(out, body) {
				t.Fatalf("secret body %q survived in %q", body, out)
			}
		}
	})
}
