package inventory

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sample sets every field of the first schema, so a dropped field shows up in a round trip.
func sample() Record {
	return Record{
		Schema:   SchemaVersion,
		Customer: Customer{ID: "acme", Name: "Acme Corp", Environment: EnvProd},
		Cluster: Cluster{
			ID:         "acme-main",
			Name:       "Acme main cluster",
			RancherURL: "https://rancher.acme.example.com/dashboard",
			Nodes: []Node{
				{ID: "srv-1", Name: "acme-srv-1", Role: RoleServer, Address: "203.0.113.11"},
				{ID: "agt-1", Name: "acme-agt-1", Role: RoleAgent, Address: "node2.acme.example.com"},
			},
			Access: Access{
				Direct:    &DirectAccess{User: "ops", Port: 22},
				Jump:      &JumpAccess{Host: "203.0.113.5", Port: 2222, User: "jump"},
				Tailscale: &TailscaleAccess{AuthKeyRef: "tailscale_auth_key"},
			},
		},
	}
}

func customerDir(t *testing.T, id string) string {
	t.Helper()
	return CustomerDir(t.TempDir(), id)
}

func TestRoundTripPreservesEveryField(t *testing.T) {
	ctx := context.Background()
	dir := customerDir(t, "acme")
	want := sample()
	if err := Save(ctx, dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := os.ReadFile(RecordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the record\n got: %+v\nwant: %+v", got, want)
	}
	if err := Save(ctx, dir, got); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	second, err := os.ReadFile(RecordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("saving a loaded record changed the bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	info, err := os.Stat(RecordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Fatalf("record mode = %04o, want %04o", perm, fileMode)
	}
}

func TestRoundTripKeepsDisabledAccessMethodsAbsent(t *testing.T) {
	ctx := context.Background()
	dir := customerDir(t, "acme")
	want := sample()
	want.Cluster.Access = Access{Jump: &JumpAccess{Host: "jump.acme.example.com", Port: 22, User: "ops"}}
	if err := Save(ctx, dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(RecordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"direct:", "tailscale:"} {
		if bytes.Contains(raw, []byte(method)) {
			t.Errorf("disabled method %q was written:\n%s", method, raw)
		}
	}
	got, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the record\n got: %+v\nwant: %+v", got, want)
	}
}

func TestDisplayNameChangeKeepsIdentity(t *testing.T) {
	ctx := context.Background()
	dir := customerDir(t, "acme")
	r := sample()
	if err := Save(ctx, dir, r); err != nil {
		t.Fatalf("Save: %v", err)
	}
	r.Customer.Name = "Acme Holding (renamed)"
	r.Cluster.Name = "Primary"
	r.Cluster.Nodes[0].Name = "srv-new-hostname"
	if err := Save(ctx, dir, r); err != nil {
		t.Fatalf("Save after rename: %v", err)
	}
	got, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Customer.ID != "acme" || got.Cluster.ID != "acme-main" || got.Cluster.Nodes[0].ID != "srv-1" {
		t.Fatalf("identity changed with display names: customer %q cluster %q node %q",
			got.Customer.ID, got.Cluster.ID, got.Cluster.Nodes[0].ID)
	}
	if got.Customer.Name != "Acme Holding (renamed)" {
		t.Fatalf("display name not stored: %q", got.Customer.Name)
	}
	entries, err := os.ReadDir(filepath.Dir(RecordPath(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != recordFile {
		t.Fatalf("record directory holds %v, want only %s", names(entries), recordFile)
	}
}

func TestLoadRefusesMalformedFiles(t *testing.T) {
	valid := "ktags_schema: 1\n" +
		"ktags_customer: {id: acme, name: Acme, environment: test}\n" +
		"ktags_cluster:\n" +
		"  id: main\n  name: Main\n  rancher_url: https://rancher.example.com\n" +
		"  nodes: [{id: srv-1, name: srv-1, role: server, address: 203.0.113.11}]\n" +
		"  access: {direct: {user: ops, port: 22}}\n"
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"unknown top-level field", valid + "ktags_extra: 1\n", "parse"},
		{"unknown nested field", strings.Replace(valid, "id: main\n", "id: main\n  password: tskey-example-leak\n", 1), "parse"},
		{"duplicate key", strings.Replace(valid, "id: main\n", "id: main\n  id: other\n", 1), "parse"},
		{"directory mismatch", strings.Replace(valid, "id: acme", "id: globex", 1), "directory name"},
		{"invalid content", strings.Replace(valid, "environment: test", "environment: dev", 1), "environment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := customerDir(t, "acme")
			writeRaw(t, dir, tt.content)
			_, err := Load(context.Background(), dir)
			if err == nil {
				t.Fatal("Load accepted the file")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "tskey-example-leak") {
				t.Fatalf("error quotes the secret: %q", err)
			}
		})
	}

	t.Run("valid baseline", func(t *testing.T) {
		dir := customerDir(t, "acme")
		writeRaw(t, dir, valid)
		if _, err := Load(context.Background(), dir); err != nil {
			t.Fatalf("baseline refused: %v", err)
		}
	})
}

func TestSaveRefusesDirectoryMismatch(t *testing.T) {
	dir := customerDir(t, "globex")
	err := Save(context.Background(), dir, sample())
	var verr *ValidationError
	if !errors.As(err, &verr) || !strings.Contains(err.Error(), "directory name globex") {
		t.Fatalf("Save error = %v, want a directory mismatch", err)
	}
	if _, statErr := os.Stat(RecordPath(dir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refused record was written: %v", statErr)
	}
}

func TestFailedWriteLeavesPreviousBytes(t *testing.T) {
	boom := errors.New("injected failure")
	tests := []struct {
		name   string
		fs     func(fileSystem) fileSystem
		record func(*Record)
	}{
		{name: "invalid record", fs: func(fs fileSystem) fileSystem { return fs },
			record: func(r *Record) { r.Cluster.Nodes[1].ID = r.Cluster.Nodes[0].ID }},
		{name: "create temp", fs: func(fs fileSystem) fileSystem {
			fs.createTemp = func(string, string) (tempFile, error) { return nil, boom }
			return fs
		}},
		{name: "chmod", fs: failingFile("chmod", boom)},
		{name: "write", fs: failingFile("write", boom)},
		{name: "sync", fs: failingFile("sync", boom)},
		{name: "close", fs: failingFile("close", boom)},
		{name: "rename", fs: func(fs fileSystem) fileSystem {
			fs.rename = func(string, string) error { return boom }
			return fs
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dir := customerDir(t, "acme")
			if err := Save(ctx, dir, sample()); err != nil {
				t.Fatalf("first Save: %v", err)
			}
			before, err := os.ReadFile(RecordPath(dir))
			if err != nil {
				t.Fatal(err)
			}

			next := sample()
			next.Customer.Name = "Changed"
			if tt.record != nil {
				tt.record(&next)
			}
			if err := tt.fs(osFS).save(ctx, dir, next); err == nil {
				t.Fatal("save reported success")
			}

			after, err := os.ReadFile(RecordPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("previous record changed\nbefore:\n%s\nafter:\n%s", before, after)
			}
			entries, err := os.ReadDir(filepath.Dir(RecordPath(dir)))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("temp file left behind: %v", names(entries))
			}
		})
	}
}

func TestSaveReportsDirectorySyncFailureAfterReplace(t *testing.T) {
	ctx := context.Background()
	dir := customerDir(t, "acme")
	fs := osFS
	fs.syncDir = func(string) error { return errors.New("injected failure") }
	err := fs.save(ctx, dir, sample())
	if err == nil || !strings.Contains(err.Error(), "replaced") || strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("error = %v, want a replaced-but-not-synced report", err)
	}
}

// failingFile makes one step of the temp file fail after the file was really created.
func failingFile(step string, err error) func(fileSystem) fileSystem {
	return func(fs fileSystem) fileSystem {
		create := fs.createTemp
		fs.createTemp = func(dir, pattern string) (tempFile, error) {
			f, createErr := create(dir, pattern)
			if createErr != nil {
				return nil, createErr
			}
			return &faultyFile{tempFile: f, step: step, err: err}, nil
		}
		return fs
	}
}

type faultyFile struct {
	tempFile
	step string
	err  error
}

func (f *faultyFile) Chmod(m os.FileMode) error {
	if f.step == "chmod" {
		return f.err
	}
	return f.tempFile.Chmod(m)
}

func (f *faultyFile) Write(p []byte) (int, error) {
	if f.step == "write" {
		// A short write reaches the disk before the failure, as a full disk would.
		n, _ := f.tempFile.Write(p[:len(p)/2])
		return n, f.err
	}
	return f.tempFile.Write(p)
}

func (f *faultyFile) Sync() error {
	if f.step == "sync" {
		return f.err
	}
	return f.tempFile.Sync()
}

func (f *faultyFile) Close() error {
	if f.step == "close" {
		_ = f.tempFile.Close()
		return f.err
	}
	return f.tempFile.Close()
}

func writeRaw(t *testing.T, dir, content string) {
	t.Helper()
	file := RecordPath(dir)
	if err := os.MkdirAll(filepath.Dir(file), dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), fileMode); err != nil {
		t.Fatal(err)
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
