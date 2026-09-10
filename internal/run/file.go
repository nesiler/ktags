package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	metaFile   = "meta.json"
	eventsFile = "events.jsonl"
	resultFile = "result.json"
	fileMode   = 0o600
	dirMode    = 0o700
)

// tempFile is the part of *os.File the atomic write uses.
type tempFile interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

// eventFile is the part of *os.File a Recorder appends events through, so tests can fail each call.
type eventFile interface {
	io.Writer
	Sync() error
	Close() error
}

// fileSystem holds the calls writeAtomic makes, so tests can fail each step.
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
		d, err := os.Open(dir) //nolint:gosec // dir is a run directory below the state root
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

// writeAtomic writes data to a temp file beside file, syncs it, renames it over file and syncs
// the directory. A failure before the rename removes the temp file, so file is either absent or
// complete; a reader never sees a half-written document under its final name.
func (fs fileSystem) writeAtomic(file string, data []byte) error {
	dir := filepath.Dir(file)
	tmp, err := fs.createTemp(dir, "."+filepath.Base(file)+".tmp-*")
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
	return fs.syncDir(dir)
}

// decodeStrict decodes one JSON document and refuses unknown fields and trailing data. The
// decoder's own message is dropped: it can quote content.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("not a complete JSON document of the expected shape")
	}
	if dec.More() {
		return errors.New("trailing data after the JSON document")
	}
	return nil
}

// eventScan is the outcome of reading events.jsonl.
type eventScan struct {
	events []Event
	// last is the ID of the last complete, valid line read.
	last uint64
	// torn is set when the file ends in a line without a newline: a crash during an append.
	torn bool
}

// readEvents returns the events with an ID above after, at most limit of them (0 means all).
// Lines must be complete, valid and numbered 1, 2, 3, …; a final line without a newline is a
// torn append and is ignored. Any other damage is an error naming the line number.
func readEvents(path string, after uint64, limit int) (eventScan, error) {
	f, err := os.Open(path) //nolint:gosec // path is a run directory below the state root
	if err != nil {
		return eventScan{}, err
	}
	defer func() { _ = f.Close() }()
	var scan eventScan
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			scan.torn = len(line) > 0
			return scan, nil
		}
		if err != nil {
			return scan, err
		}
		var event Event
		lineNo := scan.last + 1
		if err := decodeStrict(line, &event); err != nil {
			return scan, fmt.Errorf("%s line %d: %w", eventsFile, lineNo, err)
		}
		if event.Version != FormatVersion {
			return scan, fmt.Errorf("%s line %d: format version %d, want %d", eventsFile, lineNo, event.Version, FormatVersion)
		}
		if event.ID != lineNo {
			return scan, fmt.Errorf("%s line %d: event ID %d, want %d", eventsFile, lineNo, event.ID, lineNo)
		}
		scan.last = event.ID
		if event.ID > after {
			scan.events = append(scan.events, event)
			if limit > 0 && len(scan.events) == limit {
				return scan, nil
			}
		}
	}
}
