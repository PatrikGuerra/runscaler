package inbox

import (
	"fmt"
	"regexp"
	"time"
)

// EditableConfig is the closed allowlist of settings the panel may change.
// Everything else in config.toml (provider, daemon-mode, dind, socket, network,
// shared volumes, disk guard, health, log file, GitHub App) is always copied
// from the current file and can never be set through the inbox.
type EditableConfig struct {
	URL             string `json:"url"`
	Name            string `json:"name"`
	MaxRunners      int    `json:"maxRunners"`
	Concurrent      int    `json:"concurrent"`
	MinRunners      int    `json:"minRunners"`
	DockerMemoryMB  int    `json:"dockerMemoryMb"`
	DockerCPU       int    `json:"dockerCpu"`
	DockerPidsLimit int    `json:"dockerPidsLimit"`
	RunnerImage     string `json:"runnerImage"`
	DrainTimeout    string `json:"drainTimeout"`
	LogLevel        string `json:"logLevel"`
}

var (
	repoURLPattern     = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	namePattern        = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	runnerImagePattern = regexp.MustCompile(`^ghcr\.io/actions/actions-runner:[0-9]+\.[0-9]+\.[0-9]+$`)
	logLevels          = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
)

// Validate checks every field against its allowed range. It is the trust
// boundary: the panel repeats these rules only to fail fast.
func (c EditableConfig) Validate() error {
	if !repoURLPattern.MatchString(c.URL) {
		return fmt.Errorf("url: must look like https://github.com/<owner>/<repo>")
	}
	if !namePattern.MatchString(c.Name) {
		return fmt.Errorf("name: must match %s", namePattern)
	}
	if err := between("maxRunners", c.MaxRunners, 1, 2); err != nil {
		return err
	}
	if err := between("concurrent", c.Concurrent, 1, 2); err != nil {
		return err
	}
	if err := between("minRunners", c.MinRunners, 0, 1); err != nil {
		return err
	}
	if err := between("dockerMemoryMb", c.DockerMemoryMB, 256, 1536); err != nil {
		return err
	}
	if err := between("dockerCpu", c.DockerCPU, 1, 4); err != nil {
		return err
	}
	if err := between("dockerPidsLimit", c.DockerPidsLimit, 64, 4096); err != nil {
		return err
	}
	if !runnerImagePattern.MatchString(c.RunnerImage) {
		return fmt.Errorf("runnerImage: must be ghcr.io/actions/actions-runner:<x.y.z>")
	}
	d, err := time.ParseDuration(c.DrainTimeout)
	if err != nil || d < time.Minute || d > 30*time.Minute {
		// 30m keeps the drain inside the container's 31m stop_grace_period.
		return fmt.Errorf("drainTimeout: must be a duration between 1m and 30m")
	}
	if !logLevels[c.LogLevel] {
		return fmt.Errorf("logLevel: must be debug, info, warn or error")
	}
	return nil
}

func between(field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s: must be between %d and %d", field, lo, hi)
	}
	return nil
}

// apply writes the editable fields into a decoded config.toml tree.
func (c EditableConfig) apply(tree map[string]any) error {
	tree["url"] = c.URL
	tree["name"] = c.Name
	tree["max-runners"] = int64(c.MaxRunners)
	tree["concurrent"] = int64(c.Concurrent)
	tree["min-runners"] = int64(c.MinRunners)
	tree["runner-image"] = c.RunnerImage
	tree["drain-timeout"] = c.DrainTimeout
	tree["log-level"] = c.LogLevel
	docker, ok := tree["docker"].(map[string]any)
	if !ok {
		return fmt.Errorf("current config has no [docker] table")
	}
	docker["memory"] = int64(c.DockerMemoryMB)
	docker["cpu"] = int64(c.DockerCPU)
	docker["pids-limit"] = int64(c.DockerPidsLimit)
	return nil
}

// editableFrom reads the editable fields back out of a decoded config tree.
func editableFrom(tree map[string]any) EditableConfig {
	docker, _ := tree["docker"].(map[string]any)
	return EditableConfig{
		URL:             str(tree["url"]),
		Name:            str(tree["name"]),
		MaxRunners:      num(tree["max-runners"]),
		Concurrent:      num(tree["concurrent"]),
		MinRunners:      num(tree["min-runners"]),
		DockerMemoryMB:  num(docker["memory"]),
		DockerCPU:       num(docker["cpu"]),
		DockerPidsLimit: num(docker["pids-limit"]),
		RunnerImage:     str(tree["runner-image"]),
		DrainTimeout:    str(tree["drain-timeout"]),
		LogLevel:        str(tree["log-level"]),
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int {
	n, _ := v.(int64)
	return int(n)
}
