// Command secrets-age proves the customer secret-store contract of ADR-0004 with the age
// library: multi-recipient encryption, decryption by each recipient, refusal for a non-recipient,
// and re-keying that removes one operator and adds another. It never prints secret material;
// only recipient names, outcomes and whether decrypted bytes match.
package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
	"filippo.io/age/armor"
)

type operator struct {
	name string
	id   *age.X25519Identity
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	ops := map[string]*operator{}
	for _, n := range []string{"alice", "bob", "carol"} {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			return err
		}
		ops[n] = &operator{name: n, id: id}
	}
	// Synthetic store content; generated per run and never printed.
	secret := make([]byte, 32)
	if _, err := io.ReadFull(randReader{}, secret); err != nil {
		return err
	}
	plain := []byte(fmt.Sprintf("rke2_token: %x\nrancher_admin_password: %x\n", secret[:16], secret[16:]))
	want := sha256.Sum256(plain)

	store, err := encrypt(plain, ops["alice"], ops["bob"])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "store v1: recipients alice,bob; armored=%t; plaintext visible=%t\n",
		bytes.HasPrefix(store, []byte(armor.Header)), bytes.Contains(store, plain))
	for _, n := range []string{"alice", "bob", "carol"} {
		report(out, "v1", n, store, ops[n], want)
	}

	// Re-key: an operator who can decrypt rewrites the store for the new recipient list.
	got, err := decrypt(store, ops["alice"])
	if err != nil {
		return err
	}
	store2, err := encrypt(got, ops["alice"], ops["carol"])
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "store v2 (re-keyed by alice): recipients alice,carol")
	for _, n := range []string{"alice", "bob", "carol"} {
		report(out, "v2", n, store2, ops[n], want)
	}

	// Checks that make the proof fail loudly instead of printing a misleading table.
	if _, err := decrypt(store2, ops["bob"]); err == nil {
		return errors.New("removed recipient bob can still decrypt after re-key")
	}
	if _, err := decrypt(store, ops["carol"]); err == nil {
		return errors.New("non-recipient carol decrypted store v1")
	}
	for _, n := range []string{"alice", "carol"} {
		b, err := decrypt(store2, ops[n])
		if err != nil || sha256.Sum256(b) != want {
			return fmt.Errorf("%s cannot recover identical content from store v2", n)
		}
	}
	fmt.Fprintln(out, "PASS")
	return nil
}

func encrypt(plain []byte, to ...*operator) ([]byte, error) {
	var buf bytes.Buffer
	a := armor.NewWriter(&buf)
	rs := make([]age.Recipient, 0, len(to))
	for _, o := range to {
		rs = append(rs, o.id.Recipient())
	}
	w, err := age.Encrypt(a, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := a.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decrypt(store []byte, o *operator) ([]byte, error) {
	r, err := age.Decrypt(armor.NewReader(bytes.NewReader(store)), o.id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func report(out io.Writer, v, name string, store []byte, o *operator, want [32]byte) {
	b, err := decrypt(store, o)
	switch {
	case err != nil:
		var nm *age.NoIdentityMatchError
		fmt.Fprintf(out, "  %s %-5s decrypt: refused (no matching identity: %t)\n", v, name, errors.As(err, &nm))
	default:
		fmt.Fprintf(out, "  %s %-5s decrypt: ok (content identical: %t)\n", v, name, sha256.Sum256(b) == want)
	}
}
