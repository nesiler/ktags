package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"
)

const (
	// settingsFile holds the tool settings in the config root (development.md §2).
	settingsFile = "config.yml"
	// defaultHealthInterval is the time between two scheduled health runs of a customer (#65 D6).
	defaultHealthInterval = 5 * time.Minute
	// minHealthInterval and maxHealthInterval bound the setting: shorter would hammer customer
	// machines, longer would leave a stopped node green for a day.
	minHealthInterval = time.Minute
	maxHealthInterval = 24 * time.Hour
)

// settings is the content of the settings file. Unknown keys are refused.
type settings struct {
	Health struct {
		// Interval is a Go duration such as 5m.
		Interval string `yaml:"interval"`
	} `yaml:"health"`
}

// healthInterval reads health.interval from the settings file in the config root. A missing
// file or key takes the default; anything unreadable or out of bounds refuses the service start,
// so a typo never silently runs the default.
func healthInterval(configRoot string) (time.Duration, error) {
	file := filepath.Join(configRoot, settingsFile)
	data, err := os.ReadFile(file) //nolint:gosec // the path is built from the resolved config root
	if errors.Is(err, fs.ErrNotExist) {
		return defaultHealthInterval, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cannot read the settings file %s: %w", file, err)
	}
	const next = "next: fix the file; the one known key is health.interval, for example `health: {interval: 5m}`"
	var s settings
	if err := yaml.NewDecoder(bytes.NewReader(data), yaml.DisallowUnknownField()).Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("the settings file %s is refused: bad YAML or an unknown key; %s", file, next)
	}
	if s.Health.Interval == "" {
		return defaultHealthInterval, nil
	}
	d, err := time.ParseDuration(s.Health.Interval)
	if err != nil {
		return 0, fmt.Errorf("the settings file %s is refused: health.interval %q is not a duration; %s", file, s.Health.Interval, next)
	}
	if d < minHealthInterval || d > maxHealthInterval {
		return 0, fmt.Errorf("the settings file %s is refused: health.interval %s is outside %s to %s; %s", file, d, minHealthInterval, maxHealthInterval, next)
	}
	return d, nil
}
