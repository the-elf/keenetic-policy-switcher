package keenetic

import "testing"

// Reference values computed independently with Python's hashlib for the
// same login/realm/password/challenge, per spec §9.1 ("checked against
// precomputed reference values"):
//
//	md5 = MD5("admin:NDM:secretpass") = 34f8094b3aafb917d4e55b1693f39737
//	sha = SHA256("CHALLENGE1234567890" + md5)
func TestComputeAuthResponse(t *testing.T) {
	const (
		login     = "admin"
		realm     = "NDM"
		password  = "secretpass"
		challenge = "CHALLENGE1234567890"
		wantSHA   = "e01ad9b61efad3536a2f05df9226d7223114117392e690d730285a26e708e5c3"
	)

	got := computeAuthResponse(login, realm, password, challenge)
	if got != wantSHA {
		t.Fatalf("computeAuthResponse() = %q, want %q", got, wantSHA)
	}
}

func TestComputeAuthResponse_DifferentInputsDifferentHashes(t *testing.T) {
	a := computeAuthResponse("admin", "NDM", "pass1", "challengeA")
	b := computeAuthResponse("admin", "NDM", "pass2", "challengeA")
	c := computeAuthResponse("admin", "NDM", "pass1", "challengeB")

	if a == b {
		t.Error("different passwords produced the same hash")
	}
	if a == c {
		t.Error("different challenges produced the same hash")
	}
}
