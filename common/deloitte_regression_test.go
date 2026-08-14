package common

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestDeloitteRegression tallies per-network pass/fail for a Deloitte-format
// CSV given via POR_DELOITTE_CSV. Used to compare behavior before/after the
// network-table refactor; not part of the normal test suite.
func TestDeloitteRegression(t *testing.T) {
	csvPath := os.Getenv("POR_DELOITTE_CSV")
	if csvPath == "" {
		t.Skip("POR_DELOITTE_CSV not set")
	}
	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("open %s: %v", csvPath, err)
	}
	defer f.Close()

	type tally struct{ pass, fail int }
	stats := map[string]*tally{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNumber := 0
	netIdx := 1 // digitalAsset,network,address,... layout
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if lineNumber == 1 {
			cols := strings.Split(strings.ToLower(line), ",")
			if len(cols) > 1 && strings.TrimSpace(cols[1]) == "chain" {
				netIdx = 2 // digitalasset,chain,network,address,... layout
			}
			continue
		}
		fields := ParseCSVLine(line)
		if len(fields) < netIdx+6 {
			continue
		}
		get := func(i int) string {
			if i < len(fields) {
				return strings.TrimSpace(fields[i])
			}
			return ""
		}
		da, network := get(0), get(netIdx)
		addr, sm, sm2 := get(netIdx+1), get(netIdx+2), get(netIdx+3)
		msg, pk := get(netIdx+4), get(netIdx+5)
		o1, o2 := get(netIdx+6), get(netIdx+7)

		ok := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					ok = false
				}
			}()
			ok1, _, _ := verifyCSVLineMultithread(network, addr, msg, sm, sm2, pk, o1, o2, da, lineNumber, t)
			ok2, _, _ := verifyCSVLineStarknetOnly(network, addr, msg, sm, sm2, pk, da, lineNumber, t)
			ok = ok1 && ok2
		}()

		if stats[network] == nil {
			stats[network] = &tally{}
		}
		if ok {
			stats[network].pass++
		} else {
			stats[network].fail++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", csvPath, err)
	}

	networks := make([]string, 0, len(stats))
	for n := range stats {
		networks = append(networks, n)
	}
	sort.Strings(networks)
	for _, n := range networks {
		fmt.Printf("REG %s pass=%d fail=%d\n", n, stats[n].pass, stats[n].fail)
	}
}
