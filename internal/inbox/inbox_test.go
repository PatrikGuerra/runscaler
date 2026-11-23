package inbox

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baseConfig = `log-level = "info"
concurrent = 1
drain-timeout = "30m"
provider = "docker"
url = "https://github.com/owner/repo"
name = "orangepi3"
runner-image = "ghcr.io/actions/actions-runner:2.338.2"
max-runners = 1
min-runners = 0
github-app.client-id = "Iv23example"
github-app.installation-id = 42
github-app.private-key-file = "KEYPATH"

[disk]
guard = false

[docker]
socket = "/var/run/docker.sock"
daemon-mode = "none"
dind = false
memory = 1024
cpu = 2
pids-limit = 256
network = "runners"
shared-volume = ""
`

func validEditable() EditableConfig {
	return EditableConfig{
		URL: "https://github.com/owner/other", Name: "pi-2",
		MaxRunners: 2, Concurrent: 2, MinRunners: 1,
		DockerMemoryMB: 1536, DockerCPU: 4, DockerPidsLimit: 512,
		RunnerImage: "ghcr.io/actions/actions-runner:2.339.0", DrainTimeout: "10m", LogLevel: "debug",
	}
}

type harness struct {
	t                *testing.T
	opts             Options
	drains, stops    int
	validateErr      error
	validatedConfigs []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	for _, d := range []string{data, filepath.Join(dir, "exchange", "inbox"), filepath.Join(dir, "exchange", "outbox")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	keyPath := filepath.Join(data, "app.pem")
	writeFile(t, keyPath, rsaPEM(t))
	cfg := strings.ReplaceAll(baseConfig, "KEYPATH", filepath.ToSlash(keyPath))
	writeFile(t, filepath.Join(data, "config.toml"), []byte(cfg))

	h := &harness{t: t}
	h.opts = Options{
		Dir:        filepath.Join(dir, "exchange"),
		ConfigPath: filepath.Join(data, "config.toml"),
		Version:    "test",
		Drain:      func() { h.drains++ },
		StopNow:    func() { h.stops++ },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Validate: func(_ context.Context, path string) error {
			body, _ := os.ReadFile(path)
			h.validatedConfigs = append(h.validatedConfigs, string(body))
			return h.validateErr
		},
	}
	return h
}

func (h *harness) request(body string) {
	writeFile(h.t, filepath.Join(h.opts.Dir, "inbox", "request.json"), []byte(body))
}

func (h *harness) result() Result {
	var r Result
	data, err := os.ReadFile(filepath.Join(h.opts.Dir, "outbox", "result.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) configTree() map[string]any {
	tree, err := loadTree(h.opts.ConfigPath)
	if err != nil {
		h.t.Fatal(err)
	}
	return tree
}

func (h *harness) assertInboxEmpty() {
	h.t.Helper()
	for _, f := range []string{"request.json", "app.pem"} {
		if _, err := os.Stat(filepath.Join(h.opts.Dir, "inbox", f)); !errors.Is(err, os.ErrNotExist) {
			h.t.Fatalf("inbox/%s was not removed", f)
		}
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rsaPEM(t *testing.T) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func configRequest(c EditableConfig) string {
	body, _ := json.Marshal(Request{ID: "req-1", Kind: KindConfig, Config: &c})
	return string(body)
}

func TestEditableConfigValidate(t *testing.T) {
	if err := validEditable().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*EditableConfig){
		"url host":       func(c *EditableConfig) { c.URL = "https://evil.example/owner/repo" },
		"url path":       func(c *EditableConfig) { c.URL = "https://github.com/owner/repo/../x" },
		"name":           func(c *EditableConfig) { c.Name = "Bad Name" },
		"maxRunners":     func(c *EditableConfig) { c.MaxRunners = 3 },
		"concurrent":     func(c *EditableConfig) { c.Concurrent = 0 },
		"minRunners":     func(c *EditableConfig) { c.MinRunners = 2 },
		"memory":         func(c *EditableConfig) { c.DockerMemoryMB = 4096 },
		"cpu":            func(c *EditableConfig) { c.DockerCPU = 0 },
		"pids":           func(c *EditableConfig) { c.DockerPidsLimit = 10 },
		"image registry": func(c *EditableConfig) { c.RunnerImage = "docker.io/evil/runner:1.0.0" },
		"image latest":   func(c *EditableConfig) { c.RunnerImage = "ghcr.io/actions/actions-runner:latest" },
		"drain too long": func(c *EditableConfig) { c.DrainTimeout = "2h" },
		"drain garbage":  func(c *EditableConfig) { c.DrainTimeout = "soon" },
		"log level":      func(c *EditableConfig) { c.LogLevel = "trace" },
	}
	for name, mutate := range cases {
		c := validEditable()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestConfigRequestAppliesOnlyEditableFields(t *testing.T) {
	h := newHarness(t)
	h.request(configRequest(validEditable()))

	if !processOnce(context.Background(), h.opts) {
		t.Fatal("applied request should stop the watcher")
	}
	if r := h.result(); !r.OK || r.ID != "req-1" {
		t.Fatalf("result = %+v", r)
	}
	if h.drains != 1 || h.stops != 0 {
		t.Fatalf("drains=%d stops=%d", h.drains, h.stops)
	}
	tree := h.configTree()
	if got := editableFrom(tree); got != validEditable() {
		t.Fatalf("editable fields = %+v", got)
	}
	docker := tree["docker"].(map[string]any)
	if docker["daemon-mode"] != "none" || docker["dind"] != false || docker["network"] != "runners" {
		t.Fatalf("fixed docker keys changed: %+v", docker)
	}
	app := tree["github-app"].(map[string]any)
	if app["client-id"] != "Iv23example" || app["installation-id"] != int64(42) {
		t.Fatalf("github-app changed: %+v", app)
	}
	if _, err := os.Stat(h.opts.ConfigPath + ".bak"); err != nil {
		t.Fatalf("no backup: %v", err)
	}
	if len(h.validatedConfigs) != 1 || !strings.Contains(h.validatedConfigs[0], "pi-2") {
		t.Fatalf("validator did not see the new config: %v", h.validatedConfigs)
	}
	h.assertInboxEmpty()
}

func TestUnknownFieldRejectsWholeRequest(t *testing.T) {
	h := newHarness(t)
	before, _ := os.ReadFile(h.opts.ConfigPath)
	body := strings.Replace(configRequest(validEditable()), `"url"`, `"dind":true,"url"`, 1)
	h.request(body)

	if processOnce(context.Background(), h.opts) {
		t.Fatal("rejected request must not stop the watcher")
	}
	if r := h.result(); r.OK || !strings.Contains(r.Error, "dind") {
		t.Fatalf("result = %+v", r)
	}
	after, _ := os.ReadFile(h.opts.ConfigPath)
	if string(before) != string(after) || h.drains != 0 {
		t.Fatal("config changed or drain called on a rejected request")
	}
	h.assertInboxEmpty()
}

func TestFailedValidationKeepsConfig(t *testing.T) {
	h := newHarness(t)
	h.validateErr = errors.New("runner validate failed: GitHub API unreachable")
	before, _ := os.ReadFile(h.opts.ConfigPath)
	h.request(configRequest(validEditable()))

	if processOnce(context.Background(), h.opts) {
		t.Fatal("rejected request must not stop the watcher")
	}
	if r := h.result(); r.OK || !strings.Contains(r.Error, "GitHub API") {
		t.Fatalf("result = %+v", r)
	}
	after, _ := os.ReadFile(h.opts.ConfigPath)
	if string(before) != string(after) {
		t.Fatal("config changed after failed validation")
	}
	if _, err := os.Stat(h.opts.ConfigPath + ".new"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("config.toml.new left behind")
	}
	if h.drains != 0 {
		t.Fatal("drain called on a rejected request")
	}
}

func TestBadRequestsAreRejected(t *testing.T) {
	cases := map[string]string{
		"not json":       `{`,
		"bad id":         `{"id":"../x","kind":"action","action":"restart"}`,
		"unknown kind":   `{"id":"a","kind":"shell"}`,
		"unknown action": `{"id":"a","kind":"action","action":"reboot"}`,
		"missing config": `{"id":"a","kind":"config"}`,
		"too large":      `{"id":"a","kind":"action","action":"restart","x":"` + strings.Repeat("a", maxRequestBytes) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.request(body)
			if processOnce(context.Background(), h.opts) {
				t.Fatal("rejected request must not stop the watcher")
			}
			if r := h.result(); r.OK || r.Error == "" {
				t.Fatalf("result = %+v", r)
			}
			if h.drains != 0 || h.stops != 0 {
				t.Fatal("drain or stop called")
			}
			h.assertInboxEmpty()
		})
	}
}

func TestActions(t *testing.T) {
	h := newHarness(t)
	h.request(`{"id":"a","kind":"action","action":"restart"}`)
	if !processOnce(context.Background(), h.opts) || h.drains != 1 || h.stops != 0 {
		t.Fatalf("restart: drains=%d stops=%d", h.drains, h.stops)
	}

	h = newHarness(t)
	h.request(`{"id":"b","kind":"action","action":"stop-now"}`)
	if !processOnce(context.Background(), h.opts) || h.drains != 0 || h.stops != 1 {
		t.Fatalf("stop-now: drains=%d stops=%d", h.drains, h.stops)
	}
}

func TestKeyRequest(t *testing.T) {
	h := newHarness(t)
	keyPath := str(h.configTree()["github-app"].(map[string]any)["private-key-file"])
	oldKey, _ := os.ReadFile(keyPath)
	newKey := rsaPEM(t)
	writeFile(t, filepath.Join(h.opts.Dir, "inbox", "app.pem"), newKey)
	h.request(`{"id":"k1","kind":"key"}`)

	if !processOnce(context.Background(), h.opts) || !h.result().OK || h.drains != 1 {
		t.Fatalf("key request not applied: %+v", h.result())
	}
	if got, _ := os.ReadFile(keyPath); string(got) != string(newKey) {
		t.Fatal("key not replaced")
	}
	if got, _ := os.ReadFile(keyPath + ".bak"); string(got) != string(oldKey) {
		t.Fatal("old key not backed up")
	}
	h.assertInboxEmpty()
}

func TestKeyRequestRestoresOnFailedValidation(t *testing.T) {
	h := newHarness(t)
	h.validateErr = errors.New("401 Bad credentials")
	keyPath := str(h.configTree()["github-app"].(map[string]any)["private-key-file"])
	oldKey, _ := os.ReadFile(keyPath)
	writeFile(t, filepath.Join(h.opts.Dir, "inbox", "app.pem"), rsaPEM(t))
	h.request(`{"id":"k2","kind":"key"}`)

	if processOnce(context.Background(), h.opts) {
		t.Fatal("rejected key must not stop the watcher")
	}
	if r := h.result(); r.OK || !strings.Contains(r.Error, "previous key restored") {
		t.Fatalf("result = %+v", r)
	}
	if got, _ := os.ReadFile(keyPath); string(got) != string(oldKey) {
		t.Fatal("previous key not restored")
	}
}

func TestCheckRSAKeyRejectsOtherKeys(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(ec)
	cases := map[string][]byte{
		"not pem":   []byte("hello"),
		"ecdsa":     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		"encrypted": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: []byte{1}}),
		"cert type": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}),
	}
	for name, data := range cases {
		if err := checkRSAKey(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := checkRSAKey(rsaPEM(t)); err != nil {
		t.Fatalf("RSA key rejected: %v", err)
	}
}

func TestSnapshot(t *testing.T) {
	h := newHarness(t)
	if err := writeSnapshot(h.opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(h.opts.Dir, "outbox", "current-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Config.Name != "orangepi3" || s.Config.DockerMemoryMB != 1024 || s.Config.DrainTimeout != "30m" {
		t.Fatalf("config = %+v", s.Config)
	}
	if s.ReadOnly.ClientID != "Iv23example" || s.ReadOnly.InstallationID != 42 || s.ReadOnly.KeyUpdatedAt == nil {
		t.Fatalf("readOnly = %+v", s.ReadOnly)
	}
	if strings.Contains(string(data), "PRIVATE KEY") {
		t.Fatal("snapshot leaks the key")
	}
}

func TestNoRequestIsANoOp(t *testing.T) {
	h := newHarness(t)
	if processOnce(context.Background(), h.opts) {
		t.Fatal("empty inbox must not stop the watcher")
	}
	if _, err := os.Stat(filepath.Join(h.opts.Dir, "outbox", "result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("result written without a request")
	}
}
