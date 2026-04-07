package proxy

import "testing"

func TestInboundDialTargetRewritesPodIPToLoopback(t *testing.T) {
	got := inboundDialTarget("10.244.0.6:80")
	if got != "127.0.0.1:80" {
		t.Fatalf("inboundDialTarget() = %q, want %q", got, "127.0.0.1:80")
	}
}

func TestInboundDialTargetLeavesUnknownAddressUntouched(t *testing.T) {
	got := inboundDialTarget("bad-address")
	if got != "bad-address" {
		t.Fatalf("inboundDialTarget() = %q, want %q", got, "bad-address")
	}
}
