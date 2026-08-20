package keenetic

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
)

// computeAuthResponse implements Keenetic's RCI challenge-response hash:
//
//	md5 = MD5("<login>:<realm>:<password>")   (hex)
//	sha = SHA256("<challenge>" + md5)          (hex)
//
// sha is what gets sent as the "password" field of POST /auth.
func computeAuthResponse(login, realm, password, challenge string) string {
	md5Sum := md5.Sum([]byte(login + ":" + realm + ":" + password))
	md5Hex := hex.EncodeToString(md5Sum[:])

	shaSum := sha256.Sum256([]byte(challenge + md5Hex))
	return hex.EncodeToString(shaSum[:])
}
