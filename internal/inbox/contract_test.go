package inbox

import (
	"path/filepath"
	"testing"
)

// The files in testdata/contract are also copied into the Home Assistant
// integration (PatrikGuerra/ha-runscaler), whose tests check that it writes the
// same shape. If one of these stops decoding, the integration breaks too.
func TestContractFixturesDecode(t *testing.T) {
	cases := map[string]struct{ kind, action string }{
		"request-config.json":   {KindConfig, ""},
		"request-restart.json":  {KindAction, ActionRestart},
		"request-stop-now.json": {KindAction, ActionStopNow},
	}
	for file, want := range cases {
		t.Run(file, func(t *testing.T) {
			req, err := readRequest(filepath.Join("testdata", "contract", file))
			if err != nil {
				t.Fatalf("readRequest: %v", err)
			}
			if req.Kind != want.kind || req.Action != want.action {
				t.Fatalf("got kind=%q action=%q, want %q %q", req.Kind, req.Action, want.kind, want.action)
			}
			if req.Kind == KindConfig {
				if req.Config == nil {
					t.Fatal("config: missing")
				}
				if err := req.Config.Validate(); err != nil {
					t.Fatalf("Validate: %v", err)
				}
			}
		})
	}
}
