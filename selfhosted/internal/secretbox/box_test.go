package secretbox

import "testing"

func TestRoundTrip(t *testing.T) {
	box, err := New("test-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal("gateway-password")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "gateway-password" {
		t.Fatal("password was stored in plaintext")
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if opened != "gateway-password" {
		t.Fatalf("opened %q", opened)
	}
}
