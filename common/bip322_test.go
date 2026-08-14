package common

// Test vectors come from the BIP-322 specification repository:
// https://github.com/bitcoin/bips/blob/master/bip-0322/basic-test-vectors.json
//
// The spec ships two signature encodings. The vectors in
// generated-test-vectors.json carry an "smp"/"ful" variant prefix, while the
// "simple" P2TR vector below is the bare serialized witness stack. The bare
// form is what wallets and go-wallet-sdk actually produce today, and it is the
// form VerifyBip322P2TR accepts; the prefixed encoding is out of scope.
//
// Only public data is reproduced here. The spec's test private keys are
// deliberately omitted: these tests verify signatures, they never create them.

import (
	"strings"
	"testing"

	btc_chaincfg "github.com/btcsuite/btcd/chaincfg"
	"github.com/okx/go-wallet-sdk/coins/bitcoin"
)

// bip322TestNetwork mirrors the network VerifyBip322P2TR uses. Note this is
// btcsuite's chaincfg, not the martinboehm fork behind GetBTCMainNetParams().
func bip322TestNetwork() *btc_chaincfg.Params {
	return &btc_chaincfg.MainNetParams
}

// The single "simple" P2TR vector from basic-test-vectors.json.
const (
	p2trVectorAddr = "bc1pss0zhytly75awhm6x2hhvd5lnzv3vssgrf9axfheq8ldyzn88ges79fler"
	p2trVectorMsg  = "No prefix fallback"
	p2trVectorSig  = "AUCJYOwOjxYAvatTAGYaVlNXBVyFuc4MwNQkOuK2tl8xhfKDONd0NjfYyNSYcRqeCp8hsAnCEPHAVEkO9h6vbQ/R"
)

func TestVerifyBip322P2TR_OfficialVector(t *testing.T) {
	if err := VerifyBip322P2TR(p2trVectorAddr, p2trVectorMsg, p2trVectorSig); err != nil {
		t.Fatalf("official BIP-322 P2TR vector should verify, got error: %v", err)
	}
}

// A P2TR row in the shape actually published in the PoR report: the OKX
// standard message, signed by the address' own key. Public data -- a PoR
// signature is meant to be verifiable by anyone.
const (
	p2trPorAddr = "bc1pk0wjw0ygsz9zrz6d8su3hd4874wcnu66azmrw09c8nxmu3jyfk2sdfxdnj"
	p2trPorMsg  = "I am an OKX address"
	p2trPorSig  = "AUCxq+Eh2KkyIcH1fC3wHyUxKIx+mhFin6OfsvVma2xpv3LaYJgVBH+XGOOcj6mwzQAysQwW1M2bfCn3FQgCtfU5"
)

func TestVerifyBip322P2TR_PorReportVector(t *testing.T) {
	if err := VerifyBip322P2TR(p2trPorAddr, p2trPorMsg, p2trPorSig); err != nil {
		t.Fatalf("PoR report P2TR row should verify, got error: %v", err)
	}
	// Same row through the entry point the CSV verification actually uses.
	if err := VerifyUtxoCoin("BTC", p2trPorAddr, p2trPorMsg, p2trPorSig, "", ""); err != nil {
		t.Fatalf("PoR report P2TR row should verify via VerifyUtxoCoin, got error: %v", err)
	}
	if err := VerifyBip322P2TR(p2trPorAddr, "I am NOT an OKX address", p2trPorSig); err == nil {
		t.Error("PoR report P2TR row must fail on a different message, got nil error")
	}
}

func TestVerifyBip322P2TR_Rejects(t *testing.T) {
	// A different taproot address, taken from the spec's error vectors.
	const otherP2TRAddr = "bc1pyrgrm6cu6n54jrvkdjd9rvyd3xfyu84s2623awu2srn6mxhscwpsm5644w"

	// Same witness stack as the valid vector with one signature byte flipped
	// (…vbQ/R -> …vbQ/S), so it stays well-formed but no longer verifies.
	tamperedSig := p2trVectorSig[:len(p2trVectorSig)-1] + "S"

	cases := []struct {
		name    string
		addr    string
		msg     string
		sign    string
		wantErr string
	}{
		{"tampered signature", p2trVectorAddr, p2trVectorMsg, tamperedSig, "BIP-322 signature"},
		{"wrong message", p2trVectorAddr, "a message that was never signed", p2trVectorSig, "BIP-322 signature"},
		{"wrong address", otherP2TRAddr, p2trVectorMsg, p2trVectorSig, "BIP-322 signature"},
		{"empty signature", p2trVectorAddr, p2trVectorMsg, "", "empty signature"},
		{"not base64", p2trVectorAddr, p2trVectorMsg, "not-valid-base64!!!", "base64"},
		// Reaches VerifyBip322P2TR only if the caller misroutes a non-taproot
		// address; it must not be treated as a key-path spend.
		{"p2wpkh address", "bc1q9vza2e8x573nczrlzms0wvx3gsqjx7vavgkx0l", p2trVectorMsg, p2trVectorSig, "not a P2TR address"},
		{"p2pkh address", "13jTtHxBPFwZkaCdm6BwJMMJkqvTpBZccw", p2trVectorMsg, p2trVectorSig, "not a P2TR address"},
		{"garbage address", "definitely-not-an-address", p2trVectorMsg, p2trVectorSig, "failed to decode BTC address"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := VerifyBip322P2TR(c.addr, c.msg, c.sign)
			if err == nil {
				t.Fatalf("expected verification to fail, got nil error")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error should mention %q, got: %v", c.wantErr, err)
			}
		})
	}
}

// TestVerifyBip322P2TR_RejectsScriptPath guards the key-path-only restriction:
// a script-path witness stack carries more than one item and must be refused
// rather than silently misread.
func TestVerifyBip322P2TR_RejectsScriptPath(t *testing.T) {
	// "smp"-prefixed encoding from generated-test-vectors.json. Its leading
	// byte decodes as a witness item count far above 1.
	const prefixedSig = "smpAUB6B2Rbupzua8LTQIF06516wzl+cwKy1be8RgoiW0riyXdKwe6GTz/5Hnb37m67pJwIKCh+D5jDueG6KpvYpmu8"

	err := VerifyBip322P2TR("bc1pcquvhrqv0q68t4m0hfq6tpn006qrskyc7yrqnp2uyrf2emg3wynsdjyk38",
		"PURVOQ544B6HUATVBJZN5EZJUU", prefixedSig)
	if err == nil {
		t.Fatal("prefixed/script-path signature must not verify as a key-path spend")
	}
}

// TestBip322MessageHash pins the tagged message hash against the spec's
// tx_hashes vectors.
func TestBip322MessageHash(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"", "c90c269c4f8fcbe6880f72a721ddfbf1914268a794cbb21cfafee13770ae19f1"},
		{"Hello World", "f0eb03b1a75ac6d9847f55c624a99169b5dccba2a31f5b23bea77ba270de0a7a"},
		{"UTF-8 support: öäüéàè 测试文本 😄", "43936b237ea38c7794eb5d755e0d220b6db92ebfc5c8f482759d22b1286376d7"},
	}
	for _, c := range cases {
		if got := bitcoin.Bip0322Hash(c.msg); got != c.want {
			t.Errorf("Bip0322Hash(%q) = %s, want %s", c.msg, got, c.want)
		}
	}
}

// TestBip322ToSpendAndToSignTxHashes checks the two transactions BIP-322 is
// built on, so a construction error cannot hide behind a passing signature
// check. The spec's tx_hashes vectors use a P2WPKH address; the construction
// does not depend on the address type.
func TestBip322ToSpendAndToSignTxHashes(t *testing.T) {
	const addr = "bc1q9vza2e8x573nczrlzms0wvx3gsqjx7vavgkx0l"

	cases := []struct{ msg, toSpend, toSign string }{
		{"",
			"c5680aa69bb8d860bf82d4e9cd3504b55dde018de765a91bb566283c545a99a7",
			"1e9654e951a5ba44c8604c4de6c67fd78a27e81dcadcfe1edf638ba3aaebaed6"},
		{"Hello World",
			"b79d196740ad5217771c1098fc4a4b51e0535c32236c71f1ea4d61a2d603352b",
			"88737ae86f2077145f93cc4b153ae9a1cb8d56afa511988c149c5c8c9d93bddf"},
		{"UTF-8 support: öäüéàè 测试文本 😄",
			"c8f4f525fe8afb1bc09b44175bd2096f079c98425e8a1be676b712add1fb62f0",
			"8f488e06b89eafd019ec528109eafaf7f1d1811fd617aa1eeb9658f1c1be6586"},
	}

	for _, c := range cases {
		toSpend, err := bitcoin.BuildToSpend(c.msg, addr, bip322TestNetwork())
		if err != nil {
			t.Fatalf("BuildToSpend(%q) failed: %v", c.msg, err)
		}
		if toSpend != c.toSpend {
			t.Errorf("to_spend txid for %q = %s, want %s", c.msg, toSpend, c.toSpend)
		}

		toSign, _, err := buildBip322ToSign(addr, c.msg, bip322TestNetwork())
		if err != nil {
			t.Fatalf("buildBip322ToSign(%q) failed: %v", c.msg, err)
		}
		if got := toSign.TxHash().String(); got != c.toSign {
			t.Errorf("to_sign txid for %q = %s, want %s", c.msg, got, c.toSign)
		}
	}
}

func TestIsP2TRAddress(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
	}{
		{"official vector", p2trVectorAddr, true},
		{"another mainnet p2tr", "bc1pyrgrm6cu6n54jrvkdjd9rvyd3xfyu84s2623awu2srn6mxhscwpsm5644w", true},
		{"p2wpkh", "bc1q9vza2e8x573nczrlzms0wvx3gsqjx7vavgkx0l", false},
		{"p2wsh", "bc1qp0ahvfh83088w49k405szqgg4f3pptr7p2g06tdxfjcd40z4lh4q95lsz9", false},
		{"p2pkh", "13jTtHxBPFwZkaCdm6BwJMMJkqvTpBZccw", false},
		{"p2sh", "3EiFfcqMgLYbtUNssM7Vwabwrx1YqagWXc", false},
		// Detection decodes rather than prefix-matching, so a corrupted
		// checksum and a non-BTC HRP are both rejected.
		{"bad checksum", p2trVectorAddr[:len(p2trVectorAddr)-1] + "q", false},
		{"litecoin hrp", "ltc1pss0zhytly75awhm6x2hhvd5lnzv3vssgrf9axfheq8ldyzn88gesqz0jvq", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := IsP2TRAddress(c.addr); got != c.want {
			t.Errorf("IsP2TRAddress(%s) [%s] = %v, want %v", c.addr, c.name, got, c.want)
		}
	}
}

// TestGuessUtxoCoinAddressTypeP2TR locks in the fix for bc1p... addresses,
// which the "^bc1[0-9a-zA-Z]{11,71}$" pattern used to report as P2WSH.
func TestGuessUtxoCoinAddressTypeP2TR(t *testing.T) {
	cases := []struct {
		addr string
		want string
		note string
	}{
		{p2trVectorAddr, "P2TR", "the fix"},
		{"bc1pyrgrm6cu6n54jrvkdjd9rvyd3xfyu84s2623awu2srn6mxhscwpsm5644w", "P2TR", "the fix"},

		// Below: pre-existing behaviour, recorded so this change is provably
		// classification-neutral for everything that is not P2TR.
		{"bc1qp0ahvfh83088w49k405szqgg4f3pptr7p2g06tdxfjcd40z4lh4q95lsz9", "P2WSH", ""},
		{"13jTtHxBPFwZkaCdm6BwJMMJkqvTpBZccw", "P2PKH", ""},
		{"3EiFfcqMgLYbtUNssM7Vwabwrx1YqagWXc", "P2SH", ""},

		// Pre-existing defect, unchanged here and reported separately: the
		// P2WPKH branch tests len(address) == 40, but mainnet bech32 P2WPKH
		// addresses are 42 characters, so they fall through to P2WSH.
		{"bc1q9vza2e8x573nczrlzms0wvx3gsqjx7vavgkx0l", "P2WSH", "pre-existing len==40 defect"},

		// Not BTC P2TR: taproot detection is scoped to the BTC mainnet HRP,
		// and the ltc1 pattern below still yields the pre-existing P2WSH.
		{"ltc1pss0zhytly75awhm6x2hhvd5lnzv3vssgrf9axfheq8ldyzn88gesqz0jvq", "P2WSH", "non-BTC hrp"},
	}
	for _, c := range cases {
		if got := GuessUtxoCoinAddressType(c.addr); got != c.want {
			t.Errorf("GuessUtxoCoinAddressType(%s) = %q, want %q (%s)", c.addr, got, c.want, c.note)
		}
	}
}

// TestVerifyUtxoCoinRoutesP2TR checks that P2TR rows reach the BIP-322 path
// through the normal entry point instead of the pubkey-recovery path, and that
// a bad signature is reported as an error rather than silently passing.
func TestVerifyUtxoCoinRoutesP2TR(t *testing.T) {
	if err := VerifyUtxoCoin("BTC", p2trVectorAddr, p2trVectorMsg, p2trVectorSig, "", ""); err != nil {
		t.Fatalf("valid P2TR row should verify via VerifyUtxoCoin, got: %v", err)
	}

	if err := VerifyUtxoCoin("BTC", p2trVectorAddr, "not the signed message", p2trVectorSig, "", ""); err == nil {
		t.Error("P2TR row with a wrong message must fail, got nil error")
	}

	if err := VerifyUtxoCoin("BTC", p2trVectorAddr, p2trVectorMsg, "", "", ""); err == nil {
		t.Error("P2TR row with an empty signature must fail, got nil error")
	}
}
