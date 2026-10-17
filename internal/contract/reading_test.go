package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFixturesParse(t *testing.T) {
	files, err := filepath.Glob("../../contract/fixtures/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var r Reading
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if r.V != 1 {
				t.Fatalf("v = %d", r.V)
			}
			if name == "unknown-tool.json" {
				return // carries fields this contract does not model
			}
			out, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("round trip differs\nwant %v\ngot  %v", want, got)
			}
		})
	}
}
