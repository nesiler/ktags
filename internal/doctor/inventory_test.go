package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/inventory"
)

func record(id string) inventory.Record {
	return inventory.Record{
		Schema:   inventory.SchemaVersion,
		Customer: inventory.Customer{ID: id, Name: "Acme Corp", Environment: inventory.EnvTest},
		Cluster: inventory.Cluster{
			ID:         id + "-main",
			Name:       "main cluster",
			RancherURL: "https://rancher.example.com",
			Nodes:      []inventory.Node{{ID: "srv-1", Name: id + "-srv-1", Role: inventory.RoleServer, Address: "203.0.113.11"}},
			Access: inventory.Access{
				Direct: &inventory.DirectAccess{User: "ops", Port: 22},
			},
		},
	}
}

func writeRecord(t *testing.T, env Env, dir, body string) string {
	t.Helper()
	file := inventory.RecordPath(inventory.CustomerDir(env.Roots.Data.Path, dir))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestInventoryWithoutCustomers(t *testing.T) {
	r := only(t, runCheck(t, "inventory", testEnv(t)))
	want(t, r, StatusOK, "")
	if !strings.Contains(r.Evidence, "no customers yet") {
		t.Fatalf("evidence %q", r.Evidence)
	}
}

func TestInventoryLoads(t *testing.T) {
	env := testEnv(t)
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		if err := inventory.Save(ctx, inventory.CustomerDir(env.Roots.Data.Path, id), record(id)); err != nil {
			t.Fatal(err)
		}
	}
	// A file beside the customer directories is not a customer.
	if err := os.WriteFile(filepath.Join(inventory.CustomersDir(env.Roots.Data.Path), "README"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := only(t, runCheck(t, "inventory", env))
	want(t, r, StatusOK, "")
	if !strings.HasPrefix(r.Evidence, "2 customer record(s) load") {
		t.Fatalf("evidence %q", r.Evidence)
	}
}

// Every refused record is its own row with its path, the problem without the value, and the
// command that opens the file.
func TestInventoryRefusedRecords(t *testing.T) {
	env := testEnv(t)
	if err := inventory.Save(context.Background(), inventory.CustomerDir(env.Roots.Data.Path, "acme"), record("acme")); err != nil {
		t.Fatal(err)
	}
	broken := writeRecord(t, env, "globex", "rke2_token: K10aaaabbbbccccdddd::server:eeeeffff\n")
	missing := writeRecord(t, env, "initech", "")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	results := runCheck(t, "inventory", env)
	if len(results) != 2 {
		t.Fatalf("rows %+v, want one per refused record", results)
	}
	for i, tc := range []struct{ id, file, problem string }{
		{"inventory.globex", broken, "cannot parse the customer record"},
		{"inventory.initech", missing, "cannot read the customer record"},
	} {
		r := results[i]
		if r.ID != tc.id {
			t.Fatalf("row %d is %q, want %q", i, r.ID, tc.id)
		}
		want(t, r, StatusFail, "${EDITOR:-vi} "+quote(tc.file))
		if !strings.Contains(r.Evidence, tc.file+": "+tc.problem) {
			t.Fatalf("evidence %q, want the path and %q", r.Evidence, tc.problem)
		}
		if strings.Contains(r.Evidence, "K10aaaa") || strings.Contains(r.Evidence, "next:") {
			t.Fatalf("evidence %q quotes the value or a foreign next step", r.Evidence)
		}
		// The fix opens exactly that file, also with an EDITOR that carries an argument.
		opened := filepath.Join(t.TempDir(), "opened")
		editor := filepath.Join(t.TempDir(), "editor")
		if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s %s' \"$1\" \"$2\" > \""+opened+"\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		sh(t, r.Fix, "EDITOR="+editor+" -w")
		if got, err := os.ReadFile(opened); err != nil || string(got) != "-w "+tc.file {
			t.Fatalf("the editor got %q, %v; want %q", got, err, "-w "+tc.file)
		}
	}
}

func TestInventoryUnlistable(t *testing.T) {
	env := testEnv(t)
	dir := inventory.CustomersDir(env.Roots.Data.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	r := only(t, runCheck(t, "inventory", env))
	want(t, r, StatusFail, "chmod 700 "+quote(dir))
	if !strings.Contains(r.Evidence, "cannot list "+dir+": permission denied") {
		t.Fatalf("evidence %q", r.Evidence)
	}
	sh(t, r.Fix)
	want(t, only(t, runCheck(t, "inventory", env)), StatusOK, "")
}
