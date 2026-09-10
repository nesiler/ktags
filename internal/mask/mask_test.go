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
		{"password escaped json", `{\"password\":\"hunter2\",\"user\":\"ops\"}`, `{\"password\":\"[masked]\",\"user\":\"ops\"}`},
		{"token escaped json in msg", `"msg": "{\"rke2_token\": \"abc123\"}"`, `"msg": "{\"rke2_token\": \"[masked]\"}"`},
		{"token double escaped json", `{\\\"token\\\":\\\"abc\\\"}`, `{\\\"token\\\":\\\"[masked]\\\"}`},
		{"password escaped unterminated", `{\"password\":\"hunt` + "\nnext: 1", `{\"password\":\"[masked]` + "\nnext: 1"},
		{"password unterminated double quote", "password: \"hunter2\nnext: 1", "password: \"[masked]\nnext: 1"},
		{"password unterminated single quote", "password: 'hunter2\nnext: 1", "password: '[masked]\nnext: 1"},
		{"password yaml escaped single quote", "password: 'it''s' ok", "password: '[masked]' ok"},
		{"password block scalar", "password: >-\n  hunter2\n  more\nnext: 1", "password: >-\n  [masked]\nnext: 1"},
		{"token literal block with blank line", "token: |\n  abc\n\n  def\nuser: ops", "token: |\n  [masked]\nuser: ops"},
		{"token block scalar with comment", "token: |+2 # note\n    abc", "token: |+2 # note\n    [masked]"},
		{"password flag with space", "ktags run --password hunter2 --debug", "ktags run --password [masked] --debug"},
		{"token flag with space first", "--token abc123", "--token [masked]"},
		{"password flag upper case", "cmd --PASSWORD hunter2", "cmd --PASSWORD [masked]"},
		{"token short flag quoted", `cmd -rke2-token "a b" x`, `cmd -rke2-token "[masked]" x`},
		{"token flag single quoted unterminated", "cmd --token 'abc", "cmd --token '[masked]"},
		{"password block scalar crlf", "password: |\r\n  hunter2\r\nnext: 1", "password: |\r\n  [masked]\r\nnext: 1"},
		{"token block scalar crlf with blank lines", "token: |-\r\n \r\n  abc\r\n\r\n  def\r\nuser: ops", "token: |-\r\n \r\n  [masked]\r\nuser: ops"},
		{"block indicator without indented lines", "password: |\nnext: 1", "password: [masked]\nnext: 1"},
		{"two block scalars with comments", "token: |\t# c\n  abc\npassword: | # d\n  def", "token: |\t# c\n  [masked]\npassword: | # d\n  [masked]"},
		{"block scalar right after the colon", "password:>-\n  hunter2", "password:>-\n  [masked]"},
		{"password yaml tag", "password: !!str hunter2 # quoted by tag", "password: !!str [masked] # quoted by tag"},
		{"token yaml tag quoted", `token: !!str "a b"`, `token: !!str "[masked]"`},
		{"password yaml tag block", "password: !!binary |\n  aGk=", "password: !!binary |\n  [masked]"},
		{"password starting with an exclamation mark", "password: !hunter2 ok", "password: [masked] [masked]"},
		{"password starting like a core tag", "password: !!strong pw", "password: [masked] [masked]"},
		{"password bare yaml tag", "password: ! x", "password: [masked] [masked]"},
		{"password tag with a quote", "password: !x'y z", "password: [masked] [masked]"},
		{"password local yaml tag", "password: !vault |\n  abc", "password: [masked] |\n  [masked]"},
		{"password arrow quoted", `{password => "hunter2", user => "ops"}`, `{password => "[masked]", user => "ops"}`},
		{"token arrow plain", "token=>abc123", "token=>[masked]"},
		{"password single quoted pairs", "{'password': 'p1', 'user': 'ops'}", "{'password': '[masked]', 'user': 'ops'}"},
		{"key with dots, digits and dashes", "app.k8s_v1-token.b_2-x: abc", "app.k8s_v1-token.b_2-x: [masked]"},
		{"flag with dots, digits and dashes", "cmd --app.k8s_v1-token.b_2-x abc", "cmd --app.k8s_v1-token.b_2-x [masked]"},
		{"pairs inside quotes", `run "password=hunter2" 'token=abc' now`, `run "password=[masked]" 'token=[masked]' now`},
		{"flags inside quotes and after a newline", "sh -c \"--password hunter2\" '--token abc'\n--token def\nnext", "sh -c \"--password [masked]\" '--token [masked]'\n--token [masked]\nnext"},
		{"bearer after a tab", "Authorization: Bearer\tabc\nnext", "Authorization: Bearer\t[masked]\nnext"},
		{"bearer in single quotes", "-H 'Authorization: Bearer abc'", "-H 'Authorization: Bearer [masked]'"},
		{"bearer in json", `{"Authorization": "Bearer abc"}`, `{"Authorization": "Bearer [masked]"}`},
		{"rke2 token upper case hash", "K10" + strings.ToUpper(fakeHash), "[masked]"},
		{"rke2 full tokens in quotes", "join 'K10" + fakeHash + "::s:p' \"K10" + fakeHash + "::a:b\" K10" + fakeHash + "::c:d\nnext", "join '[masked]' \"[masked]\" [masked]\nnext"},
		{"rke2 token shortest hash", "K10" + fakeHash[:20] + " ok", "[masked] ok"},
		{"rke2 token next to a word", "K10" + fakeHash + ":ready", "[masked]:ready"},
		{"tailscale key with digits and underscore", "tskey-client-k1_2A-b3 ok", "[masked] ok"},
		{"two pem blocks", "-----BEGIN X-----\na\n-----END X----- mid -----BEGIN Y509 KEY-----\nb\n-----END Y509 KEY----- tail", "[masked] mid [masked] tail"},
		{"escaped json with a backslash escape", `{\"password\":\"a\tb\",\"user\":\"ops\"}`, `{\"password\":\"[masked]\",\"user\":\"ops\"}`},
		{"escaped json with a raw quote", `{\"password\":\"a"b\"}`, `{\"password\":\"[masked]\"}`},
		{"block header with two digits", "token: |-10\n  abc", "token: |-10\n  [masked]"},
		{"block scalar with tab indentation", "password: |\n\tabc\n\tdef\nnext: 1", "password: |\n\t[masked]\nnext: 1"},
		{"separator between tabs", "password\t=\thunter2", "password\t=\t[masked]"},
		{"core tag before a tab", "password: !!str\thunter2", "password: !!str\t[masked]"},
		{"other tag before a tab", "password: !x\ty", "password: [masked]\t[masked]"},
		{"tag glued to a quote", "password: !\"abc\"", "password: [masked]\""},
		{"flag with a tab", "cmd --token\tabc", "cmd --token\t[masked]"},
		{"pem with an empty end label", "-----BEGIN -----\nx\n-----END ----- tail", "[masked] tail"},
		{"two tailscale keys", "tskey-a and tskey-b", "[masked] and [masked]"},
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
		"K10ServiceAccountControllerReady",
		"K10" + fakeHash[:19],
		"bearerless mode",
		"ktags --token \nnext",
		"a bearer",
		"Bearer\n",
		"node acme-srv-1 (203.0.113.11:22) ready",
		"ssh_port: 22",
		"ünïcödé ✓ text",
		`password: ""`,
		"password: ''",
		`{\"token\":\"\"}`,
		"ktags secret rotate --token --debug",
		"the rke2-token expired",
		"--password",
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
		{"adjacent secrets", "-----BEGIN X-----\na\n-----END X-----tskey-abc", "[masked]"},
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
	`{\"password\":\"s3cr3tval\"}`:      "s3cr3tval",
	"password: \"unclosedval":           "unclosedval",
	"token: >-\n  blockvalue":           "blockvalue",
	"--password flagvalue":              "flagvalue",
	"password: |\r\n  crlfvalue":        "crlfvalue",
	"password: !!str taggedval":         "taggedval",
	`password => "arrowval"`:            "arrowval",
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
