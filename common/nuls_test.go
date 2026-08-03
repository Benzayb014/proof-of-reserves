package common

import "testing"

// Vectors are real rows from the Deloitte dq-por export (message DTT_OKC_AA26,
// OKX message header, EVM-style 0x hex signatures, no public key column).
const nulsMsg = "DTT_OKC_AA26"

var nulsRows = []struct {
	addr string
	sign string
}{
	{
		"NULSd6HgUHs6nPh7YR3NG27kNLH77LkNRN2gP",
		"0x8b7f0a5c2dac59cb60b47ce1b97b99c5417c81f6046cc5a4cccfb20f31c208b47b1520453b541da4e0f21c96831371ba4ecc7c8fe4b7271f4d47b4395a2ed6f31b",
	},
	{
		"NULSd6HgVWuYkqWAdPzQ3rPsiaWko8s4znHbL",
		"0x8cb3f65a77b451ab1b976252a323bc1dcb8faf42a1ac19587fc89b99f561c03c6e2c56495d48ef1885bc34ab704edae03480b1094ed096b796866012e7dec38a1b",
	},
}

func TestVerifyEcdsaCoinNULS(t *testing.T) {
	for _, r := range nulsRows {
		if err := VerifyEcdsaCoin("NULS", r.addr, nulsMsg, r.sign); err != nil {
			t.Errorf("valid NULS row should verify, addr:%s, err:%v", r.addr, err)
		}
	}
}

func TestVerifyEcdsaCoinNULSRejectsWrongAddress(t *testing.T) {
	if err := VerifyEcdsaCoin("NULS", nulsRows[0].addr, nulsMsg, nulsRows[1].sign); err == nil {
		t.Error("signature of another account must not verify")
	}
}

func TestVerifyEcdsaCoinNULSRejectsWrongMessage(t *testing.T) {
	if err := VerifyEcdsaCoin("NULS", nulsRows[0].addr, "wrong message", nulsRows[0].sign); err == nil {
		t.Error("signature over a different message must not verify")
	}
}
