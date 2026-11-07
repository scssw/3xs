package service

import (
	"slices"
	"testing"
)

func TestFilterUsableSANs(t *testing.T) {
	sans := []string{"amd.com", "www.amd.com", "*.amd.com", "other.amd.com"}
	filtered := filterUsableSANs(sans, "www.amd.com")

	if slices.Contains(filtered, "amd.com") {
		t.Errorf("expected apex domain 'amd.com' to be filtered out, got %v", filtered)
	}
	if !slices.Contains(filtered, "www.amd.com") {
		t.Errorf("expected 'www.amd.com' in filtered, got %v", filtered)
	}
	if filtered[0] != "www.amd.com" {
		t.Errorf("expected 'www.amd.com' to be first, got %v", filtered)
	}
}
