package common

// Cardano Shelley address verification.
//
// Address vectors are real mainnet PoR rows (addresses and public keys are
// public data). Header bytes follow CIP-19: the high nibble of the first
// payload byte is the address type, the low nibble the network id.

import (
	"strings"
	"testing"
)

// Base addresses (addr1q..., type 0) from the PoR report: payment credential
// plus staking credential.
var adaBaseRows = []struct {
	addr   string
	pubKey string
}{
	{
		"addr1q88fuc9m5gavm98ke06hk5euyyjtacg92ztczscpeg2p9yws55k96g9qx3ln80qvnc4lwq47gdfp6aez3rkr7xe7m8pqydvh2w",
		"0xc666701bb25bbd2deb18a146d92c62869a404c8cf050faf8814757ce357833ea",
	},
	{
		"addr1qyrpxc74j4u5sxgclezx3263q6rw2ffut5n77n4jzx3kg8wjayugn48flvluz5q329hdgvnkwff9x5turx3hj63p6a3q6d4dk6",
		"0x9acf47e5ca33acf2350b23a1a8dfb70b9ea384e2b70689e6e44e84906f43bd1b",
	},
	{
		"addr1q8fud30vje9vcp54gue73kf4t22r5asp7zu0yv89zwzesgvd2fnfcp2nu3l493c3cvsy3dml0y90mxgxk93tsf66pczqkhnvdz",
		"0x748d9b5bec2dc1315964f455ecd4b21907f5d4ef8dd51c53f9d9a5a3c48b4080",
	},
}

func TestAdaAddressMatchesPubKey_BaseAddress(t *testing.T) {
	for _, r := range adaBaseRows {
		matched, err := AdaAddressMatchesPubKey(r.addr, r.pubKey)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", r.addr, err)
			continue
		}
		if !matched {
			t.Errorf("payment credential should match for %s", r.addr)
		}
	}
}

// TestAdaAddressMatchesPubKey_EnterpriseAddress covers the address shape
// GetAdaAddressFromPublicKey builds, so both are checked by the same path.
func TestAdaAddressMatchesPubKey_EnterpriseAddress(t *testing.T) {
	const pubKey = "0xc666701bb25bbd2deb18a146d92c62869a404c8cf050faf8814757ce357833ea"

	enterprise, err := GetAdaAddressFromPublicKey(pubKey)
	if err != nil {
		t.Fatalf("GetAdaAddressFromPublicKey failed: %v", err)
	}
	if !strings.HasPrefix(enterprise, "addr1v") {
		t.Fatalf("expected an addr1v enterprise address, got %s", enterprise)
	}
	matched, err := AdaAddressMatchesPubKey(enterprise, pubKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("enterprise address should match the key it was built from")
	}
}

// TestAdaAddressMatchesPubKey_WrongKey: a well-formed address whose payment
// credential belongs to a different key must not match, and must report no
// error -- the address itself is valid.
func TestAdaAddressMatchesPubKey_WrongKey(t *testing.T) {
	matched, err := AdaAddressMatchesPubKey(adaBaseRows[0].addr, adaBaseRows[1].pubKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Error("address must not match a different key's payment credential")
	}
}

func TestAdaAddressMatchesPubKey_Rejects(t *testing.T) {
	const pubKey = "0xc666701bb25bbd2deb18a146d92c62869a404c8cf050faf8814757ce357833ea"
	paymentHash := calculateBlake2b224(MustDecode(pubKey))

	// Same payment credential, but a header this code must refuse.
	mustEncode := func(t *testing.T, header byte, body []byte) string {
		t.Helper()
		s, err := encodeBech32("addr", append([]byte{header}, body...))
		if err != nil {
			t.Fatalf("encodeBech32 failed: %v", err)
		}
		return s
	}
	stakePart := make([]byte, adaPaymentKeyHashLen) // dummy staking credential

	// Valid bech32 under a different prefix, so the hrp check is what rejects
	// it rather than the checksum.
	stakeHrpAddr, err := encodeBech32("stake", append([]byte{0xe1}, paymentHash...))
	if err != nil {
		t.Fatalf("encodeBech32 failed: %v", err)
	}

	cases := []struct {
		name    string
		addr    string
		wantErr string
	}{
		// Type 0, network id 0 -> testnet.
		{"testnet", mustEncode(t, 0x00, append(append([]byte{}, paymentHash...), stakePart...)), "not a mainnet address"},
		// Type 1: payment credential is a script hash, not a key hash.
		{"base script hash", mustEncode(t, 0x11, append(append([]byte{}, paymentHash...), stakePart...)), "no payment key hash"},
		// Type 7: enterprise with a script hash.
		{"enterprise script hash", mustEncode(t, 0x71, paymentHash), "no payment key hash"},
		// Payload shorter than a credential hash.
		{"short payload", mustEncode(t, 0x01, paymentHash[:10]), "too short"},
		{"not bech32", "addr1thisisnotvalidbech32", "invalid bech32 address"},
		{"wrong hrp", stakeHrpAddr, "unexpected address prefix"},
		{"empty", "", "invalid bech32 address"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			matched, err := AdaAddressMatchesPubKey(c.addr, pubKey)
			if matched {
				t.Error("must not match")
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error should mention %q, got: %v", c.wantErr, err)
			}
		})
	}
}

func TestAdaAddressMatchesPubKey_BadPubKey(t *testing.T) {
	addr := adaBaseRows[0].addr
	for _, pk := range []string{"", "0xdeadbeef", "not-hex"} {
		if _, err := AdaAddressMatchesPubKey(addr, pk); err == nil {
			t.Errorf("public key %q should be rejected", pk)
		}
	}
}

// TestVerifyEd25519CoinADA drives the rows through the entry point the CSV
// verification uses, signature included.
func TestVerifyEd25519CoinADA(t *testing.T) {
	rows := []struct {
		addr, sign, pubKey string
	}{
		{
			adaBaseRows[0].addr,
			"0x1b4ec5d2bb0c50d5075958add50f77f13bf94c580cbfabdf5f831ee88c82dd68cf81cb084991a3656db2847154a59de1ee837d93da22e9692c8042a8bbafde0a",
			adaBaseRows[0].pubKey,
		},
		{
			adaBaseRows[1].addr,
			"0x854e741fb5618ecd02ebd768a20f4fbbdad47bb1ce089503aa4bbc85b0a006b4c268508846b135b230607e41c9f1752d48967a7b91679b42d0da164f3d34e201",
			adaBaseRows[1].pubKey,
		},
		{
			adaBaseRows[2].addr,
			"0x40fc3b32972191b4535cbc2dbf3d27277f24436d5aefba326be1d2e746f3dd2e7b9ee5dea426bbbfca60439b34340b5c22f7a03ab022e26efc5f1b2e531e6c01",
			adaBaseRows[2].pubKey,
		},
	}
	const msg = "DTT_OKC_AA26"

	for _, r := range rows {
		if err := VerifyEd25519Coin("ADA", r.addr, msg, r.sign, r.pubKey); err != nil {
			t.Errorf("ADA base address row should verify, addr:%s, error:%v", r.addr, err)
		}
	}

	// A staking credential the key cannot derive must not become a way to pass
	// with the wrong payment key.
	if err := VerifyEd25519Coin("ADA", rows[0].addr, msg, rows[1].sign, rows[1].pubKey); err == nil {
		t.Error("row must fail when the address belongs to a different key")
	}
	// Wrong message: the signature check must reject before any address logic.
	if err := VerifyEd25519Coin("ADA", rows[0].addr, "wrong message", rows[0].sign, rows[0].pubKey); err == nil {
		t.Error("row must fail on a different message")
	}
}
