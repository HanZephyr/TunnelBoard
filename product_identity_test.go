package main

import "testing"

func TestProductIdentity(t *testing.T) {
	if applicationTitle != "TunnelBoard" {
		t.Fatalf("applicationTitle = %q", applicationTitle)
	}
	if singleInstanceID != "tunnelboard-single-instance" {
		t.Fatalf("singleInstanceID = %q", singleInstanceID)
	}
}
