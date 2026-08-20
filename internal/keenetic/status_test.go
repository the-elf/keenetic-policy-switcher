package keenetic

import (
	"os"
	"testing"
)

func TestCheckRCIErrors_Success(t *testing.T) {
	body := readTestdata(t, "write-response-success.json")
	if err := checkRCIErrors(body); err != nil {
		t.Fatalf("checkRCIErrors() on a successful response returned an error: %v", err)
	}
}

func TestCheckRCIErrors_Error(t *testing.T) {
	body := readTestdata(t, "write-response-error.json")
	err := checkRCIErrors(body)
	if err == nil {
		t.Fatal("checkRCIErrors() on a status:error response returned nil, expected an error")
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return body
}
