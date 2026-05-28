package state

import (
	"encoding/json"
	"reflect"
	"testing"
)

// readEnabled extracts the enabledPlugins map from settings bytes.
func readEnabled(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	ep := map[string]bool{}
	if v, ok := raw["enabledPlugins"]; ok {
		if err := json.Unmarshal(v, &ep); err != nil {
			t.Fatalf("unmarshal enabledPlugins: %v", err)
		}
	}
	return ep
}

func TestToggleEnabledPlugin_NilInput(t *testing.T) {
	out, err := toggleEnabledPlugin(nil, "foo")
	if err != nil {
		t.Fatalf("toggleEnabledPlugin(nil): %v", err)
	}
	ep := readEnabled(t, out)
	if !reflect.DeepEqual(ep, map[string]bool{"foo": true}) {
		t.Errorf("enabledPlugins = %v, want {foo:true}", ep)
	}
}

func TestToggleEnabledPlugin_EnableThenDisable(t *testing.T) {
	out, err := toggleEnabledPlugin(nil, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if got := readEnabled(t, out); !got["foo"] {
		t.Fatalf("after enable, expected foo=true, got %v", got)
	}

	out2, err := toggleEnabledPlugin(out, "foo")
	if err != nil {
		t.Fatal(err)
	}
	got := readEnabled(t, out2)
	if _, present := got["foo"]; present {
		t.Errorf("after disable, expected foo absent from enabledPlugins, got %v", got)
	}
}

func TestToggleEnabledPlugin_PreservesUnknownFields(t *testing.T) {
	in := []byte(`{
  "enabledPlugins": {"foo": true},
  "effortLevel": "high",
  "statusLine": {"command": "echo hi", "nested": {"key": [1, 2, 3]}}
}`)
	out, err := toggleEnabledPlugin(in, "bar")
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// Unknown fields must survive verbatim.
	for _, key := range []string{"effortLevel", "statusLine"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing preserved field %q", key)
		}
	}

	// effortLevel value should be intact.
	var effort string
	if err := json.Unmarshal(raw["effortLevel"], &effort); err != nil {
		t.Fatalf("unmarshal effortLevel: %v", err)
	}
	if effort != "high" {
		t.Errorf("effortLevel = %q, want %q", effort, "high")
	}

	// statusLine nested structure should be intact.
	var sl struct {
		Command string `json:"command"`
		Nested  struct {
			Key []int `json:"key"`
		} `json:"nested"`
	}
	if err := json.Unmarshal(raw["statusLine"], &sl); err != nil {
		t.Fatalf("unmarshal statusLine: %v", err)
	}
	if sl.Command != "echo hi" || !reflect.DeepEqual(sl.Nested.Key, []int{1, 2, 3}) {
		t.Errorf("statusLine corrupted: %+v", sl)
	}

	// Plugin state: foo was unchanged, bar got enabled.
	ep := readEnabled(t, out)
	if !ep["foo"] || !ep["bar"] {
		t.Errorf("enabledPlugins = %v, want both foo and bar true", ep)
	}
}

func TestToggleEnabledPlugin_DisableUsesAbsentConvention(t *testing.T) {
	in := []byte(`{"enabledPlugins": {"foo": true}}`)
	out, err := toggleEnabledPlugin(in, "foo")
	if err != nil {
		t.Fatal(err)
	}
	// Disable must remove the key entirely, not set it to false. Claude Code's convention
	// is present+true = enabled, absent = disabled.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	var ep map[string]bool
	if err := json.Unmarshal(raw["enabledPlugins"], &ep); err != nil {
		t.Fatal(err)
	}
	if _, present := ep["foo"]; present {
		t.Errorf("disabled plugin should be absent from map, got %v", ep)
	}
}

func TestToggleEnabledPlugin_InvalidJSON(t *testing.T) {
	_, err := toggleEnabledPlugin([]byte("{not json"), "foo")
	if err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}
