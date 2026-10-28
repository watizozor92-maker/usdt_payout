package tron

import "testing"

func TestValidAddress(t *testing.T) {
	if !ValidAddress("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t") {
		t.Fatal("known USDT contract rejected")
	}
	if ValidAddress("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj60") {
		t.Fatal("bad checksum accepted")
	}
}
