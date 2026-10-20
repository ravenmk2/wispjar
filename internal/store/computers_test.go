package store

import (
	"encoding/json"
	"testing"
)

func TestPersistentModelJSONDefaults(t *testing.T) {
	computer := Computer{}
	volume := UserVolume{}
	run := ComputerRun{}
	op := ResourceOperation{}
	for _, err := range []error{computer.BeforeSave(nil), volume.BeforeSave(nil), run.BeforeSave(nil), op.BeforeSave(nil)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []json.RawMessage{computer.Config, volume.Policy, volume.Usage, run.Execution, run.Manifest, op.Progress} {
		if string(value) != "{}" {
			t.Fatalf("invalid default %s", value)
		}
	}
	computer.Config = json.RawMessage(`broken`)
	if computer.BeforeSave(nil) == nil {
		t.Fatal("invalid JSON accepted")
	}
}
