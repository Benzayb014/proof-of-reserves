package common

// Layout handling for the PoR report's detail section, which ships in three
// shapes. The 12-column format inserts a Type column right after coin, shifting
// every later column right by one; the 9- and 11-column formats do not.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectPorFormatOffset(t *testing.T) {
	cases := []struct {
		name   string
		header []string
		want   int
	}{
		{"9-column", []string{"coin", "network", "snapshot height", "address", "balance", "message", "signature1", "signature2", "redeem script"}, 0},
		{"11-column", []string{"coin", "Network", "Snapshot Height", "address", "amount", "message", "signature1", "signature2", "redeem script/ public key", " eoa1", " eoa2"}, 0},
		{"12-column", []string{"coin", "Type", "Network", "Snapshot Height", "address", "amount", "message", "signature1", "signature2", "redeem script/ public key", "EOA1", "EOA2"}, 1},
		{"type column padded", []string{"coin", "  Type  ", "Network"}, 1},
		{"type column lowercase", []string{"coin", "type", "Network"}, 1},
		{"summary header", []string{"coin", "amount"}, 0},
		{"empty", nil, 0},
	}
	for _, c := range cases {
		if got := DetectPorFormatOffset(c.header); got != c.want {
			t.Errorf("DetectPorFormatOffset(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

func writeTempCSV(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "por.csv")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write temp csv: %v", err)
	}
	return path
}

// TestInitPorCsvDataMapFormats is the regression guard for the defect where
// InitPorCsvDataMap only accepted rows of exactly 9 columns, so the 11- and
// 12-column reports yielded zero records.
func TestInitPorCsvDataMapFormats(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"9-column",
			"coin,amount\nBTC,1.5\n\n" +
				"coin,network,snapshot height,address,balance,message,signature1,signature2,redeem script\n" +
				"BTC,BTC,956934,addr-one,1.5,I am an OKX address,sig1,sig2,script-x\n",
		},
		{
			"11-column",
			"coin,amount\nBTC,1.5\n\n" +
				"coin,Network,Snapshot Height,address,amount,message,signature1,signature2,redeem script/ public key, eoa1, eoa2\n" +
				"BTC,BTC,956934,addr-one,1.5,I am an OKX address,sig1,sig2,script-x,eoa-1,eoa-2\n",
		},
		{
			"12-column",
			"coin,amount\nBTC,1.5\n\n" +
				"coin,Type,Network,Snapshot Height,address,amount,message,signature1,signature2,redeem script/ public key,EOA1,EOA2\n" +
				"BTC,Non Staking,BTC,956934,addr-one,1.5,I am an OKX address,sig1,sig2,script-x,eoa-1,eoa-2\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := InitPorCsvDataMap(writeTempCSV(t, c.body))
			if err != nil {
				t.Fatalf("InitPorCsvDataMap failed: %v", err)
			}
			if len(m) != 1 {
				t.Fatalf("expected 1 record, got %d: %v", len(m), m)
			}
			d, ok := m["BTC:addr-one"]
			if !ok {
				t.Fatalf("record BTC:addr-one missing, got %v", m)
			}
			// Every field must land on the same value regardless of layout.
			if d.Network != "BTC" {
				t.Errorf("Network = %q, want %q", d.Network, "BTC")
			}
			if d.SnapshotHeight != "956934" {
				t.Errorf("SnapshotHeight = %q, want %q", d.SnapshotHeight, "956934")
			}
			if d.Balance != "1.5" {
				t.Errorf("Balance = %q, want %q", d.Balance, "1.5")
			}
			if d.Message != "I am an OKX address" {
				t.Errorf("Message = %q, want %q", d.Message, "I am an OKX address")
			}
			if d.Sign1 != "sig1" || d.Sign2 != "sig2" {
				t.Errorf("Sign1/Sign2 = %q/%q, want sig1/sig2", d.Sign1, d.Sign2)
			}
			if d.Script != "script-x" {
				t.Errorf("Script = %q, want %q", d.Script, "script-x")
			}
		})
	}
}

// TestInitPorCsvDataMapSkipsSummaryAndShortRows checks that the summary section
// contributes no records and that a row truncated below the layout its header
// declared is skipped rather than parsed with shifted fields.
func TestInitPorCsvDataMapSkipsSummaryAndShortRows(t *testing.T) {
	body := "coin,amount\nBTC,1.5\nETH,2\nADA,3\n\n" +
		"coin,Type,Network,Snapshot Height,address,amount,message,signature1,signature2,redeem script/ public key,EOA1,EOA2\n" +
		"BTC,Non Staking,BTC,956934,addr-good,1.5,I am an OKX address,sig1,sig2,script-x,,\n" +
		// 9 columns under a 12-column header: one short of 9+1, must be skipped.
		"BTC,Non Staking,BTC,956934,addr-short,1.5,I am an OKX address,sig1,sig2\n"

	m, err := InitPorCsvDataMap(writeTempCSV(t, body))
	if err != nil {
		t.Fatalf("InitPorCsvDataMap failed: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected only the well-formed row, got %d records: %v", len(m), m)
	}
	if _, ok := m["BTC:addr-good"]; !ok {
		t.Errorf("well-formed row missing, got %v", m)
	}
	if _, ok := m["BTC:addr-short"]; ok {
		t.Error("truncated row must not be parsed")
	}
}

// TestInitPorCsvDataMapKeepsFirstDuplicate pins the pre-existing behaviour that
// the first occurrence of a coin:address pair wins.
func TestInitPorCsvDataMapKeepsFirstDuplicate(t *testing.T) {
	body := "coin,amount\nBTC,1.5\n\n" +
		"coin,Type,Network,Snapshot Height,address,amount,message,signature1,signature2,redeem script/ public key,EOA1,EOA2\n" +
		"BTC,Non Staking,BTC,956934,addr-one,1.5,I am an OKX address,sig1,sig2,script-x,,\n" +
		"BTC,Non Staking,BTC,956934,addr-one,9.9,I am an OKX address,sigA,sigB,script-y,,\n"

	m, err := InitPorCsvDataMap(writeTempCSV(t, body))
	if err != nil {
		t.Fatalf("InitPorCsvDataMap failed: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 record, got %d", len(m))
	}
	if got := m["BTC:addr-one"].Balance; got != "1.5" {
		t.Errorf("Balance = %q, want the first occurrence %q", got, "1.5")
	}
}
