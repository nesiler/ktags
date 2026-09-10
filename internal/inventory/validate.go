package inventory

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNameRunes = 100

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)
	userPattern    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	keyRefPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	hostnameLabel  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	rke2Token      = regexp.MustCompile(`K10[0-9a-f]{20,}`)
	credentialPair = regexp.MustCompile(`(?i)(token|password|passwd|secret|api[_-]?key)\s*[:=]`)
)

// secretMarkers are fragments that only appear in credential material (security.md §1).
var secretMarkers = []string{"tskey-", "AGE-SECRET-KEY-", "-----BEGIN", "Bearer "}

// Validate checks a record against the first schema. It reports every refused field, never the
// value, so the error is safe to show even when a secret was pasted into the record.
func Validate(r Record) error {
	v := validator{}
	if r.Schema != SchemaVersion {
		v.add("ktags_schema", fmt.Sprintf("must be %d", SchemaVersion))
	}

	v.id("ktags_customer.id", r.Customer.ID)
	v.name("ktags_customer.name", r.Customer.Name)
	switch r.Customer.Environment {
	case EnvProd, EnvStaging, EnvTest:
	default:
		v.add("ktags_customer.environment", "must be one of prod, staging, test")
	}

	c := r.Cluster
	v.id("ktags_cluster.id", c.ID)
	v.name("ktags_cluster.name", c.Name)
	v.rancherURL("ktags_cluster.rancher_url", c.RancherURL)

	if len(c.Nodes) == 0 {
		v.add("ktags_cluster.nodes", "must list at least one node")
	}
	seen := make(map[string]int, len(c.Nodes))
	for i, n := range c.Nodes {
		field := fmt.Sprintf("ktags_cluster.nodes[%d]", i)
		v.id(field+".id", n.ID)
		if first, dup := seen[n.ID]; dup && n.ID != "" {
			v.add(field+".id", fmt.Sprintf("duplicates the id of ktags_cluster.nodes[%d]", first))
		} else {
			seen[n.ID] = i
		}
		v.name(field+".name", n.Name)
		switch n.Role {
		case RoleServer, RoleAgent:
		default:
			v.add(field+".role", "must be server or agent")
		}
		v.host(field+".address", n.Address)
	}

	a := c.Access
	if d := a.Direct; d != nil {
		v.user("ktags_cluster.access.direct.user", d.User)
		v.port("ktags_cluster.access.direct.port", d.Port)
	}
	if j := a.Jump; j != nil {
		v.host("ktags_cluster.access.jump.host", j.Host)
		v.port("ktags_cluster.access.jump.port", j.Port)
		v.user("ktags_cluster.access.jump.user", j.User)
	}
	if t := a.Tailscale; t != nil {
		v.keyRef("ktags_cluster.access.tailscale.auth_key_ref", t.AuthKeyRef)
	}

	if len(v.problems) > 0 {
		return &ValidationError{Problems: v.problems}
	}
	return nil
}

type validator struct {
	problems []FieldProblem
}

func (v *validator) add(field, problem string) {
	v.problems = append(v.problems, FieldProblem{Field: field, Problem: problem})
}

// secret refuses a value that looks like credential material. It runs first on every free-text
// field so the refusal names the real reason rather than a format rule.
func (v *validator) secret(field, value string) bool {
	for _, marker := range secretMarkers {
		if strings.Contains(value, marker) {
			v.add(field, "looks like a secret; store it with the secret store and reference its key name")
			return true
		}
	}
	if rke2Token.MatchString(value) || credentialPair.MatchString(value) {
		v.add(field, "looks like a secret; store it with the secret store and reference its key name")
		return true
	}
	return false
}

func (v *validator) id(field, value string) {
	if v.secret(field, value) {
		return
	}
	if !idPattern.MatchString(value) {
		v.add(field, "must be 2-40 characters of a-z, 0-9 and '-', starting and ending with a letter or digit")
	}
}

func (v *validator) name(field, value string) {
	if v.secret(field, value) {
		return
	}
	switch {
	case strings.TrimSpace(value) == "":
		v.add(field, "must not be empty")
	case strings.TrimSpace(value) != value:
		v.add(field, "must not start or end with white space")
	case !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0:
		v.add(field, "must be printable text")
	case utf8.RuneCountInString(value) > maxNameRunes:
		v.add(field, fmt.Sprintf("must be at most %d characters", maxNameRunes))
	}
}

func (v *validator) user(field, value string) {
	if v.secret(field, value) {
		return
	}
	if !userPattern.MatchString(value) {
		v.add(field, "must be a login name: a-z, 0-9, '_' and '-', starting with a letter or '_'")
	}
}

func (v *validator) keyRef(field, value string) {
	if v.secret(field, value) {
		return
	}
	if !keyRefPattern.MatchString(value) {
		v.add(field, "must name a secret-store key: a-z, 0-9 and '_', starting with a letter")
	}
}

func (v *validator) port(field string, value int) {
	if value < 1 || value > 65535 {
		v.add(field, "must be a port between 1 and 65535")
	}
}

// host accepts an IP address or a DNS hostname, without port, scheme or path.
func (v *validator) host(field, value string) {
	if v.secret(field, value) {
		return
	}
	if !validHost(value) {
		v.add(field, "must be an IP address or a hostname, without port, scheme or path")
	}
}

func validHost(value string) bool {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Zone() == ""
	}
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.ToLower(value), ".") {
		if !hostnameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// rancherURL accepts an https URL with a host and an optional path. Credentials, queries and
// fragments are refused: they are where tokens hide in pasted URLs.
func (v *validator) rancherURL(field, value string) {
	if v.secret(field, value) {
		return
	}
	u, err := url.Parse(value)
	switch {
	case err != nil || u.Scheme != "https" || u.Opaque != "":
		v.add(field, "must be an https URL such as https://rancher.example.com")
	case u.User != nil:
		v.add(field, "looks like a secret (credentials in the URL); store them with the secret store")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		v.add(field, "must not contain a query or fragment")
	case !validHost(u.Hostname()):
		v.add(field, "must name a valid host")
	case u.Port() != "":
		if !validPort(u.Port()) {
			v.add(field, "must use a port between 1 and 65535")
		}
	}
}

func validPort(s string) bool {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || fmt.Sprint(n) != s {
		return false
	}
	return n >= 1 && n <= 65535
}

// DeriveID returns the default ID for a display name: lower case, runs of other characters
// collapsed to '-', trimmed to the ID length. The result may still be refused (for example a
// name without any letter or digit); the operator then chooses the ID.
func DeriveID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	id := strings.TrimRight(b.String(), "-")
	if len(id) > 40 {
		id = strings.TrimRight(id[:40], "-")
	}
	return id
}
