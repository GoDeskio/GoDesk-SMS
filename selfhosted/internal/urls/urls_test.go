package urls

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	got, err := NormalizeBaseURL("https://phone.example:8080/", false)
	if err != nil || got != "https://phone.example:8080" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := NormalizeBaseURL("http://10.1.2.3:8080", false); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeBaseURL("http://169.254.169.254/", false); err == nil {
		t.Fatal("expected link-local metadata address to be rejected")
	}
	if _, err := NormalizeBaseURL("https://phone.example/api", false); err == nil {
		t.Fatal("expected a path to be rejected for device origins")
	}
	got, err = NormalizeBaseURL("https://hooks.example/sms", true)
	if err != nil || got != "https://hooks.example/sms" {
		t.Fatalf("webhook url %q err %v", got, err)
	}
}
