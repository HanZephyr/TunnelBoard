package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSelfCheckReturnsMachineReadableSessionHelperContract(t *testing.T) {
	var output bytes.Buffer
	handled, err := runSelfCheck([]string{"--self-check", "--json"}, &output)
	if err != nil || !handled {
		t.Fatalf("runSelfCheck = (%v, %v)", handled, err)
	}
	var result selfCheckResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.ProtocolVersion == "" || result.PersistentService {
		t.Fatalf("unsafe self-check result: %+v", result)
	}
}

func TestSelfCheckRejectsPartialOrUnknownArguments(t *testing.T) {
	for _, args := range [][]string{{"--self-check"}, {"--json"}, {"--self-check", "--json", "extra"}} {
		var output bytes.Buffer
		handled, err := runSelfCheck(args, &output)
		if err != nil || handled || output.Len() != 0 {
			t.Fatalf("runSelfCheck(%q) = (%v, %v, %q)", args, handled, err, output.String())
		}
	}
}
