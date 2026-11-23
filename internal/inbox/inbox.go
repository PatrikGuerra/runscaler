// Package inbox applies change requests that the panel container drops into a
// shared directory. It runs inside the runscaler process, which owns the Docker
// socket, so it is the trust boundary: the panel can only ask, and every
// request is validated here before anything changes.
//
// Layout under Options.Dir:
//
//	inbox/request.json        one pending request (panel writes, inbox deletes)
//	inbox/app.pem             new GitHub App key, only for kind "key"
//	outbox/result.json        result of the last request
//	outbox/current-config.json editable fields + read-only info, written on start
package inbox

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	maxRequestBytes = 16 << 10
	maxKeyBytes     = 64 << 10
	validateTimeout = 2 * time.Minute

	KindConfig = "config"
	KindKey    = "key"
	KindAction = "action"

	ActionRestart = "restart"
	ActionStopNow = "stop-now"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// Request is the content of inbox/request.json.
type Request struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	CreatedAt time.Time       `json:"createdAt"`
	Config    *EditableConfig `json:"config,omitempty"`
	Action    string          `json:"action,omitempty"`
}

// Result is the content of outbox/result.json.
type Result struct {
	ID         string    `json:"id"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	FinishedAt time.Time `json:"finishedAt"`
}

// Snapshot is the content of outbox/current-config.json. The panel cannot read
// config.toml, so this is how it shows the current values.
type Snapshot struct {
	Config    EditableConfig `json:"config"`
	ReadOnly  ReadOnlyInfo   `json:"readOnly"`
	WrittenAt time.Time      `json:"writtenAt"`
}

// ReadOnlyInfo is shown by the panel but can never be changed through it.
type ReadOnlyInfo struct {
	ClientID         string     `json:"clientId"`
	InstallationID   int64      `json:"installationId"`
	KeyUpdatedAt     *time.Time `json:"keyUpdatedAt,omitempty"`
	RunscalerVersion string     `json:"runscalerVersion"`
}

// Options wires the inbox to the running manager.
type Options struct {
	Dir        string
	ConfigPath string
	Version    string
	// Drain starts the same graceful shutdown as SIGTERM; StopNow the same as SIGINT.
	Drain   func()
	StopNow func()
	Logger  *slog.Logger
	// Validate checks a config file. Nil runs `<this binary> validate --config <path>`
	// in a subprocess, because config loading uses process-global viper state.
	Validate     func(ctx context.Context, configPath string) error
	PollInterval time.Duration
}

// Run writes the snapshot, then polls the inbox until ctx ends or an applied
// request has asked the manager to restart.
func Run(ctx context.Context, o Options) {
	if o.PollInterval <= 0 {
		o.PollInterval = 2 * time.Second
	}
	if o.Validate == nil {
		o.Validate = validateWithSelf
	}
	for _, sub := range []string{"inbox", "outbox"} {
		if err := os.MkdirAll(filepath.Join(o.Dir, sub), 0o770); err != nil {
			o.Logger.Warn("inbox: cannot create directory", "dir", sub, "err", err)
		}
	}
	if err := writeSnapshot(o); err != nil {
		o.Logger.Warn("inbox: cannot write current-config.json", "err", err)
	}
	o.Logger.Info("inbox: watching for panel requests", "dir", o.Dir)

	ticker := time.NewTicker(o.PollInterval)
	defer ticker.Stop()
	for {
		if processOnce(ctx, o) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// processOnce handles at most one pending request. It reports true when the
// manager was asked to stop, after which no further request is read.
func processOnce(ctx context.Context, o Options) (stopped bool) {
	reqPath := filepath.Join(o.Dir, "inbox", "request.json")
	if _, err := os.Stat(reqPath); errors.Is(err, os.ErrNotExist) {
		return false
	}
	// Always clear the inbox, so a bad request cannot loop forever.
	defer func() {
		_ = os.Remove(reqPath)
		_ = os.Remove(filepath.Join(o.Dir, "inbox", "app.pem"))
	}()

	req, err := readRequest(reqPath)
	after := func() {}
	if err == nil {
		after, err = handle(ctx, o, req)
	}
	res := Result{ID: req.ID, OK: err == nil, FinishedAt: time.Now().UTC()}
	if err != nil {
		res.Error = err.Error()
		o.Logger.Warn("inbox: request rejected", "id", req.ID, "kind", req.Kind, "err", err)
	} else {
		o.Logger.Info("inbox: request applied", "id", req.ID, "kind", req.Kind)
	}
	if werr := writeJSON(filepath.Join(o.Dir, "outbox", "result.json"), res); werr != nil {
		o.Logger.Warn("inbox: cannot write result.json", "err", werr)
	}
	if err != nil {
		return false
	}
	if err := writeSnapshot(o); err != nil {
		o.Logger.Warn("inbox: cannot write current-config.json", "err", err)
	}
	after()
	return true // every applied request ends with a drain or stop
}

func readRequest(path string) (Request, error) {
	data, err := readLimited(path, maxRequestBytes)
	if err != nil {
		return Request{}, err
	}
	var req Request
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Request{}, fmt.Errorf("invalid request.json: %w", err)
	}
	if !requestIDPattern.MatchString(req.ID) {
		return Request{Kind: req.Kind}, fmt.Errorf("id: must match %s", requestIDPattern)
	}
	return req, nil
}

// handle applies one request and returns what to call after the result is
// written: the drain or stop that makes Docker restart the container.
func handle(ctx context.Context, o Options, req Request) (func(), error) {
	switch req.Kind {
	case KindConfig:
		if req.Config == nil {
			return nil, errors.New("config: missing")
		}
		return o.Drain, applyConfig(ctx, o, *req.Config)
	case KindKey:
		return o.Drain, applyKey(ctx, o)
	case KindAction:
		switch req.Action {
		case ActionRestart:
			return o.Drain, nil
		case ActionStopNow:
			return o.StopNow, nil
		}
		return nil, fmt.Errorf("action: must be %q or %q", ActionRestart, ActionStopNow)
	}
	return nil, fmt.Errorf("kind: must be %q, %q or %q", KindConfig, KindKey, KindAction)
}

func applyConfig(ctx context.Context, o Options, c EditableConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	tree, err := loadTree(o.ConfigPath)
	if err != nil {
		return err
	}
	if _, multi := tree["scaleset"]; multi {
		return errors.New("config uses [[scaleset]] entries; the panel edits only single scale set configs")
	}
	if err := c.apply(tree); err != nil {
		return err
	}
	out, err := toml.Marshal(tree)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	// Keep the extension: `runner validate` picks the config type from it (viper).
	ext := filepath.Ext(o.ConfigPath)
	newPath := strings.TrimSuffix(o.ConfigPath, ext) + ".new" + ext
	if err := writeAtomic(newPath, out, 0o600); err != nil {
		return err
	}
	if err := o.Validate(ctx, newPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	if err := copyFile(o.ConfigPath, o.ConfigPath+".bak", 0o600); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("backup config: %w", err)
	}
	return os.Rename(newPath, o.ConfigPath)
}

func applyKey(ctx context.Context, o Options) error {
	key, err := readLimited(filepath.Join(o.Dir, "inbox", "app.pem"), maxKeyBytes)
	if err != nil {
		return err
	}
	if err := checkRSAKey(key); err != nil {
		return err
	}
	tree, err := loadTree(o.ConfigPath)
	if err != nil {
		return err
	}
	app, _ := tree["github-app"].(map[string]any)
	keyPath := str(app["private-key-file"])
	if keyPath == "" {
		return errors.New("config has no github-app.private-key-file")
	}
	backup := keyPath + ".bak"
	if err := copyFile(keyPath, backup, 0o600); err != nil {
		return fmt.Errorf("backup key: %w", err)
	}
	if err := writeAtomic(keyPath, key, 0o600); err != nil {
		return err
	}
	if err := o.Validate(ctx, o.ConfigPath); err != nil {
		if rerr := copyFile(backup, keyPath, 0o600); rerr != nil {
			return fmt.Errorf("new key rejected (%v) and restoring the previous key failed: %w", err, rerr)
		}
		return fmt.Errorf("new key rejected, previous key restored: %w", err)
	}
	return nil
}

// checkRSAKey accepts only an unencrypted RSA private key, the format GitHub
// generates for Apps.
func checkRSAKey(data []byte) error {
	block, _ := pem.Decode(data)
	if block == nil {
		return errors.New("key: not a PEM file")
	}
	if strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		return errors.New("key: must not be encrypted")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
			return fmt.Errorf("key: %w", err)
		}
		return nil
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return fmt.Errorf("key: %w", err)
		}
		if _, ok := k.(*rsa.PrivateKey); !ok {
			return errors.New("key: must be an RSA key")
		}
		return nil
	}
	return fmt.Errorf("key: unexpected PEM type %q", block.Type)
}

func writeSnapshot(o Options) error {
	tree, err := loadTree(o.ConfigPath)
	if err != nil {
		return err
	}
	app, _ := tree["github-app"].(map[string]any)
	ro := ReadOnlyInfo{
		ClientID:         str(app["client-id"]),
		RunscalerVersion: o.Version,
	}
	ro.InstallationID, _ = app["installation-id"].(int64)
	if keyPath := str(app["private-key-file"]); keyPath != "" {
		if st, err := os.Stat(keyPath); err == nil {
			t := st.ModTime().UTC()
			ro.KeyUpdatedAt = &t
		}
	}
	return writeJSON(filepath.Join(o.Dir, "outbox", "current-config.json"), Snapshot{
		Config:    editableFrom(tree),
		ReadOnly:  ro,
		WrittenAt: time.Now().UTC(),
	})
}

func validateWithSelf(ctx context.Context, configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "validate", "--config", configPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("runner validate failed: %s", tail(out, 2000))
	}
	return nil
}

func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

func loadTree(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var tree map[string]any
	if err := toml.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return tree, nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", filepath.Base(path), limit)
	}
	return data, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// Outbox files hold no secrets and must be readable by the panel's user.
	return writeAtomic(path, append(data, '\n'), 0o644)
}

func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeAtomic(dst, data, perm)
}

// writeAtomic writes through a temp file in the same directory and renames it,
// so a reader never sees a half-written file.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
