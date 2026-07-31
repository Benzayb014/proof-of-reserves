package common

// BIP-322 "simple" message signature verification for P2TR (taproot) addresses.
//
// Unlike the rest of this package, which builds on the martinboehm/btcutil fork
// to cover the many UTXO altcoins, this file uses upstream btcsuite/btcd. The
// two chaincfg.Params types are not interchangeable, so GetBTCMainNetParams()
// must not be used here -- BIP-322 is BTC-only anyway.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"

	btcec "github.com/btcsuite/btcd/btcec/v2"
	btcec_schnorr "github.com/btcsuite/btcd/btcec/v2/schnorr"
	btc_util "github.com/btcsuite/btcd/btcutil"
	btc_chaincfg "github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btc_txscript "github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/okx/go-wallet-sdk/coins/bitcoin"
)

var (
	ErrNotP2TRAddress  = errors.New("not a P2TR address")
	ErrBip322Signature = errors.New("can't verify BIP-322 signature")
)

// IsP2TRAddress reports whether addr is a BTC mainnet P2TR address, i.e. a
// bech32m address with witness version 1 and a 32-byte program.
//
// Address strings are not matched by prefix: decoding also rejects a bad
// checksum, and the BTC mainnet HRP keeps lookalikes from other UTXO chains
// (ltc1p..., for instance) from being taken for taproot.
func IsP2TRAddress(addr string) bool {
	decoded, err := btc_util.DecodeAddress(addr, &btc_chaincfg.MainNetParams)
	if err != nil {
		return false
	}
	_, ok := decoded.(*btc_util.AddressTaproot)
	return ok
}

// VerifyBip322P2TR verifies a BIP-322 "simple" signature over msg for a P2TR
// address, as a taproot key-path spend.
//
// The 32-byte payload of a P2TR address *is* the tweaked x-only public key, so
// the signature is checked against the key the address itself commits to. That
// is why no public key argument is needed: there is no externally supplied key
// to trust, and a signature made by any other key cannot pass.
func VerifyBip322P2TR(addr, msg, sign string) error {
	network := &btc_chaincfg.MainNetParams

	tweakedPubKey, err := p2trTweakedPubKey(addr, network)
	if err != nil {
		return err
	}

	toSign, prevOutFetcher, err := buildBip322ToSign(addr, msg, network)
	if err != nil {
		return err
	}

	if sign == "" {
		return fmt.Errorf("%w: empty signature, addr:%s", ErrBip322Signature, addr)
	}
	witnessBytes, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return fmt.Errorf("failed to base64-decode BIP-322 signature, addr:%s, error:%v", addr, err)
	}

	// The BIP-322 simple signature is the serialized witness stack of the
	// to_sign transaction's only input.
	if err := bitcoin.BtcDecodeWitnessForBip0322(bytes.NewReader(witnessBytes), 0,
		wire.WitnessEncoding, toSign); err != nil {
		return fmt.Errorf("failed to decode BIP-322 witness stack, addr:%s, error:%v", addr, err)
	}
	witness := toSign.TxIn[0].Witness
	// A key-path spend carries exactly one stack item: the Schnorr signature.
	// Script-path spends (control block + script) are not supported.
	if len(witness) != 1 {
		return fmt.Errorf("%w: expected 1 witness item for a P2TR key-path spend, got %d, addr:%s",
			ErrBip322Signature, len(witness), addr)
	}

	sigBytes, hashType, err := splitTaprootSig(witness[0], addr)
	if err != nil {
		return err
	}

	sigHashes := btc_txscript.NewTxSigHashes(toSign, prevOutFetcher)
	sigHash, err := btc_txscript.CalcTaprootSignatureHash(sigHashes, hashType, toSign, 0, prevOutFetcher)
	if err != nil {
		return fmt.Errorf("failed to calculate taproot signature hash, addr:%s, error:%v", addr, err)
	}

	sig, err := btcec_schnorr.ParseSignature(sigBytes)
	if err != nil {
		return fmt.Errorf("failed to parse Schnorr signature, addr:%s, error:%v", addr, err)
	}
	if !sig.Verify(sigHash, tweakedPubKey) {
		return fmt.Errorf("%w: addr:%s", ErrBip322Signature, addr)
	}
	return nil
}

// p2trTweakedPubKey decodes addr and returns the tweaked x-only public key it
// commits to, rejecting anything that is not P2TR.
func p2trTweakedPubKey(addr string, network *btc_chaincfg.Params) (*btcec.PublicKey, error) {
	decoded, err := btc_util.DecodeAddress(addr, network)
	if err != nil {
		return nil, fmt.Errorf("failed to decode BTC address, addr:%s, error:%v", addr, err)
	}
	taprootAddr, ok := decoded.(*btc_util.AddressTaproot)
	if !ok {
		return nil, fmt.Errorf("%w, addr:%s", ErrNotP2TRAddress, addr)
	}
	pubKey, err := btcec_schnorr.ParsePubKey(taprootAddr.ScriptAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to parse taproot tweaked public key, addr:%s, error:%v", addr, err)
	}
	return pubKey, nil
}

// buildBip322ToSign reconstructs the BIP-322 to_spend and to_sign transactions
// for msg/addr, returning the to_sign transaction and a fetcher for its only
// previous output.
func buildBip322ToSign(addr, msg string, network *btc_chaincfg.Params) (
	*wire.MsgTx, btc_txscript.PrevOutputFetcher, error) {

	// Resolved first: bitcoin.BuildToSpend discards this error and returns
	// ("", nil), so calling it directly would report success on a bad address.
	pkScript, err := bitcoin.AddrToPkScript(addr, network)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build pkScript, addr:%s, error:%v", addr, err)
	}

	toSpendTxID, err := bitcoin.BuildToSpend(msg, addr, network)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build BIP-322 to_spend tx, addr:%s, error:%v", addr, err)
	}
	if toSpendTxID == "" {
		return nil, nil, fmt.Errorf("failed to build BIP-322 to_spend tx, addr:%s", addr)
	}
	toSpendHash, err := chainhash.NewHashFromStr(toSpendTxID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse BIP-322 to_spend txid, addr:%s, error:%v", addr, err)
	}

	// to_sign spends to_spend:0 and pays a single zero-value OP_RETURN output.
	prevOut := wire.NewOutPoint(toSpendHash, 0)
	opReturn := []byte{btc_txscript.OP_RETURN}
	packet, err := bitcoin.NewPsbt([]*wire.OutPoint{prevOut},
		[]*wire.TxOut{wire.NewTxOut(0, opReturn)},
		0, 0, []uint32{0}, bitcoin.Bip0322Opt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build BIP-322 to_sign tx, addr:%s, error:%v", addr, err)
	}

	prevOutFetcher := btc_txscript.NewMultiPrevOutFetcher(map[wire.OutPoint]*wire.TxOut{
		*prevOut: wire.NewTxOut(0, pkScript),
	})
	return packet.UnsignedTx, prevOutFetcher, nil
}

// splitTaprootSig splits a BIP-341 signature into its 64 signature bytes and
// sighash type. A bare 64-byte signature means SIGHASH_DEFAULT; a 65-byte one
// carries the type in its final byte and, per BIP-341, must not spell out
// SIGHASH_DEFAULT there.
func splitTaprootSig(raw []byte, addr string) ([]byte, btc_txscript.SigHashType, error) {
	switch len(raw) {
	case 64:
		return raw, btc_txscript.SigHashDefault, nil
	case 65:
		hashType := btc_txscript.SigHashType(raw[64])
		if hashType == btc_txscript.SigHashDefault {
			return nil, 0, fmt.Errorf("%w: 65-byte signature must not encode SIGHASH_DEFAULT, addr:%s",
				ErrBip322Signature, addr)
		}
		return raw[:64], hashType, nil
	default:
		return nil, 0, fmt.Errorf("%w: invalid Schnorr signature length %d, addr:%s",
			ErrBip322Signature, len(raw), addr)
	}
}
