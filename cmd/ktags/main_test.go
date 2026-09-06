package main

import "testing"

func TestVersionDefault(t *testing.T) {
	if version == "" {
		t.Fatal("version must have a default so `ktags version` never prints an empty string")
	}
}
