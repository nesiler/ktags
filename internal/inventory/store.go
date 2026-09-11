package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	customersDir = "customers"
	recordFile   = "ktags.yml"
	fileMode     = 0o600
	dirMode      = 0o700
)

var recordHeader = []byte("# Written by ktags: customer and cluster identity, nodes, access. No secrets.\n")

// CustomersDir is the directory below the ktags data root that holds one directory per customer.
func CustomersDir(dataRoot string) string {
	return filepath.Join(dataRoot, customersDir)
}

// CustomerDir is the inventory directory of a customer below the ktags data root.
func CustomerDir(dataRoot, customerID string) string {
	return filepath.Join(CustomersDir(dataRoot), customerID)
}

// RecordPath is the ktags.yml file inside a customer directory.
func RecordPath(customerDir string) string {
	return filepath.Join(customerDir, "group_vars", "all", recordFile)
}

// KnownHostsPath is the file of pinned host keys inside a customer directory (ansible.md §4).
func KnownHostsPath(customerDir string) string {
	return filepath.Join(customerDir, "known_hosts")
}

// Load reads and validates the record of the customer directory dir. Unknown fields, duplicate
// keys and a customer id that differs from the directory name are refused.
func Load(ctx context.Context, dir string) (Record, error) {
	file := RecordPath(dir)
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(file) //nolint:gosec // the path is built from the resolved data root
	if err != nil {
		return Record{}, &Error{File: file, Problem: "cannot read the customer record", Next: "check that the customer exists and the file is readable", Err: err}
	}
	var r Record
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.DisallowUnknownField(), yaml.CustomUnmarshaler(strictInt))
	if err := dec.Decode(&r); err != nil {
		// The decoder quotes the offending source line; drop it so a misplaced secret stays out
		// of the message. Only the position survives.
		return Record{}, &Error{File: file, Problem: "cannot parse the customer record" + position(err), Next: "fix the YAML syntax, write numbers without quotes, or remove the unknown or duplicate key"}
	}
	// A second document would escape the strict decoding above and be dropped by the next Save.
	if err := dec.Decode(&Record{}); !errors.Is(err, io.EOF) {
		return Record{}, &Error{File: file, Problem: "the customer record holds more than one YAML document" + position(err), Next: "remove everything after the first document"}
	}
	if err := check(dir, r); err != nil {
		return Record{}, &Error{File: file, Problem: "the customer record is refused", Next: "fix the listed fields in the file", Err: err}
	}
	return r, nil
}

// Save validates r and then replaces the record of the customer directory dir atomically: the
// previous file stays byte-identical unless the new one is completely written and synced.
func Save(ctx context.Context, dir string, r Record) error {
	return osFS.save(ctx, dir, r)
}

func (fs fileSystem) save(ctx context.Context, dir string, r Record) error {
	file := RecordPath(dir)
	if err := check(dir, r); err != nil {
		return &Error{File: file, Problem: "the customer record is refused", Next: "correct the listed fields and save again", Err: err}
	}
	body, err := yaml.Marshal(r)
	if err != nil {
		return &Error{File: file, Problem: "cannot encode the customer record", Next: "report this as a bug", Err: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), dirMode); err != nil {
		return &Error{File: file, Problem: "cannot create the record directory", Next: "check the permissions of the customer directory", Err: err}
	}
	err = fs.replace(file, append(bytes.Clone(recordHeader), body...))
	var synced *dirSyncError
	switch {
	case errors.As(err, &synced):
		return &Error{File: file, Problem: "the customer record was replaced but its directory could not be synced", Next: "check the disk, then save again to be sure the change is durable", Err: synced.err}
	case err != nil:
		return &Error{File: file, Problem: "cannot write the customer record; the previous file is unchanged", Next: "check free space and permissions, then save again", Err: err}
	}
	return nil
}

// dirSyncError marks a failure after the rename: the new record is in place but may not be
// durable, so the operator must not be told the previous file survived.
type dirSyncError struct{ err error }

func (e *dirSyncError) Error() string { return "sync directory: " + e.err.Error() }

// check validates r and binds it to its directory.
func check(dir string, r Record) error {
	err := Validate(r)
	if base := filepath.Base(filepath.Clean(dir)); r.Customer.ID != "" && base != r.Customer.ID {
		mismatch := FieldProblem{Field: "ktags_customer.id", Problem: "must equal the customer directory name " + base}
		var verr *ValidationError
		if errors.As(err, &verr) {
			verr.Problems = append(verr.Problems, mismatch)
			return verr
		}
		return &ValidationError{Problems: []FieldProblem{mismatch}}
	}
	return err
}

var plainInt = regexp.MustCompile(`^[0-9]+$`)

// strictInt refuses a number the decoder would otherwise coerce: quoted ("1"), tagged (!!str 1),
// float (1.0), hex (0x1) or signed (+1). Only a plain decimal scalar fills an int field.
func strictInt(n *int, raw []byte) error {
	s := strings.TrimSpace(string(raw))
	if !plainInt.MatchString(s) {
		return errors.New("must be a plain decimal number")
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return errors.New("number out of range")
	}
	*n = v
	return nil
}

func position(err error) string {
	if err == nil {
		return ""
	}
	var syntax yaml.Error
	if errors.As(err, &syntax) && syntax.GetToken() != nil {
		p := syntax.GetToken().Position
		return fmt.Sprintf(" at line %d, column %d", p.Line, p.Column)
	}
	return ""
}

// tempFile is the part of *os.File the atomic write uses.
type tempFile interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

// fileSystem holds the calls replace makes, so tests can fail each step.
type fileSystem struct {
	createTemp func(dir, pattern string) (tempFile, error)
	rename     func(oldpath, newpath string) error
	remove     func(name string) error
	syncDir    func(dir string) error
}

var osFS = fileSystem{
	createTemp: func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) },
	rename:     os.Rename,
	remove:     os.Remove,
	syncDir: func(dir string) error {
		d, err := os.Open(dir) //nolint:gosec // dir is the record's own directory
		if err != nil {
			return err
		}
		syncErr := d.Sync()
		if err := d.Close(); syncErr == nil {
			syncErr = err
		}
		return syncErr
	},
}

// replace writes data to a temp file beside file, syncs it, renames it over file and syncs the
// directory so the rename survives a crash. Any failure before the rename removes the temp file
// and leaves file untouched.
func (fs fileSystem) replace(file string, data []byte) (err error) {
	dir := filepath.Dir(file)
	tmp, err := fs.createTemp(dir, "."+recordFile+".tmp-*")
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = tmp.Close()
			_ = fs.remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(fileMode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fs.rename(tmp.Name(), file); err != nil {
		return err
	}
	renamed = true
	if err := fs.syncDir(dir); err != nil {
		return &dirSyncError{err: err}
	}
	return nil
}
