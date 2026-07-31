# Network 驱动验签 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 验签方案（算法/地址类型/消息头）由 CSV 的 Network 列决定，删除三张以 coin 为 key 的手工大表，新 token 上线零配置。

**Architecture:** 新增 `common/network.go`：归一化函数 + 一张 network→方案表（~200 行）+ 地址类型/消息头的推导函数。`common/verify.go` 内部查表全部切换；月度流程（`cmd/verifyaddress/main.go`）改用 Network 列分发；德勤流程（`common/csv_verify.go`）换表即可（它传入的本来就是 network 值）。最后删除旧表，用改造前二进制的输出做回归对比。

**Tech Stack:** Go（仓库现有依赖，无新依赖）。

**Spec:** `docs/superpowers/specs/2026-07-31-network-based-verification-design.md`

## Global Constraints

- 归一化规则：`strings.ToUpper` + `-` 折叠为 `_`；表 key 一律存 `_` 形式。
- 不改 `cmd/checkbalance`、`common/address.go`、`common/csv.go`（它们不引用三张验签表）。
- `Verify*` 导出函数名不变，只把参数 `coin` 改名 `network`；**错误信息的格式字符串不改**（仅变量取值变化），保证回归 diff 干净。
- `PorCoinUnitMap`、`PorCoinBaseUnitPrecisionMap`、两个 BlackList 保持 coin 为 key，不动。
- 工作区已有未提交改动（BIP-322/ADA 相关）；每个任务只 `git add` 自己触碰的文件，禁止 `git add -A`。
- 回归基线 = Task 1 时刻的工作区（含未提交改动），不是 HEAD。
- 金样目录：`GOLD=/private/tmp/claude-502/-Users-oker-meili-zhiwei-li-dacs-at-okg-com-117-Downloads-code-proof-of-reserves/9b960432-1518-4034-90c7-6e7c96ba0b31/scratchpad/por-regression`
- 大文件命令加长超时（`okx_por_2026070700.csv` 有 20 万行，跑一遍约几分钟）。

---

### Task 1: 回归基线采集（月度二进制金样 + 德勤回归测试）

**Files:**
- Create: `common/deloitte_regression_test.go`
- Create（金样，不入库）: `$GOLD/*.baseline.txt`

**Interfaces:**
- Produces: 环境变量驱动的回归测试 `TestDeloitteRegression`（`POR_DELOITTE_CSV=<path>` 时运行，否则 skip），按 network 输出 `REG <network> pass=<n> fail=<m>` 行。
- Produces: 金样文件，Task 7 用来对比。

- [ ] **Step 1: 建金样目录并编译基线二进制**

```bash
GOLD=/private/tmp/claude-502/-Users-oker-meili-zhiwei-li-dacs-at-okg-com-117-Downloads-code-proof-of-reserves/9b960432-1518-4034-90c7-6e7c96ba0b31/scratchpad/por-regression
mkdir -p "$GOLD"
go build -o "$GOLD/verifyaddress-baseline" ./cmd/verifyaddress
```

Expected: 编译成功（当前工作区能编译；若失败先停下报告，不要修）。

- [ ] **Step 2: 跑两个月度样本，存排序后的金样**

输出里 map 遍历顺序不确定，所以必须 `sort` 后再存：

```bash
"$GOLD/verifyaddress-baseline" --por_csv_filename example/okx_por_2026070700.csv | sort > "$GOLD/monthly-11col.baseline.txt"
"$GOLD/verifyaddress-baseline" --por_csv_filename example/okx_por_2026070700_V6.csv | sort > "$GOLD/monthly-12col.baseline.txt"
wc -l "$GOLD"/monthly-*.baseline.txt
```

Expected: 两个非空金样文件。记录每个文件里 ` accoounts, ` 行（per-coin 通过/失败统计，注意源码里就是这个拼写）的内容，后续对比的硬指标。

- [ ] **Step 3: 写德勤回归测试**

创建 `common/deloitte_regression_test.go`。要点：按表头自动识别两种德勤布局（含 `chain` 列的 dq-por 布局 network 在下标 2；否则在下标 1）；每行同时过 `verifyCSVLineMultithread`（跳 STARK）与 `verifyCSVLineStarknetOnly`（只 STARK），两者都 true 才算 pass；按 network 汇总排序输出。

```go
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
```

- [ ] **Step 4: 跑三个德勤样本，存金样**

```bash
POR_DELOITTE_CSV=example/德勤POR签名_DTT_OKC_AA26.csv go test ./common/ -run TestDeloitteRegression -v 2>&1 | grep '^REG ' > "$GOLD/deloitte-aa26.baseline.txt"
POR_DELOITTE_CSV="example/dq-por Sheet1.csv" go test ./common/ -run TestDeloitteRegression -v 2>&1 | grep '^REG ' > "$GOLD/deloitte-dqpor.baseline.txt"
POR_DELOITTE_CSV=example/test.csv go test ./common/ -run TestDeloitteRegression -v 2>&1 | grep '^REG ' > "$GOLD/deloitte-test.baseline.txt"
wc -l "$GOLD"/deloitte-*.baseline.txt
```

Expected: 三个非空金样。dq-por 覆盖 ~130 个 network（多数走 ECDSA 兜底，pass/fail 都可能有——只求前后一致，不求全过）。

- [ ] **Step 5: Commit**

```bash
git add common/deloitte_regression_test.go
git commit -m "test: add env-gated Deloitte regression tally for network refactor"
```

---

### Task 2: `common/network.go` — 归一化 + network 表 + 推导函数（TDD）

**Files:**
- Create: `common/network.go`
- Test: `common/network_test.go`

**Interfaces:**
- Produces（后续任务全靠这五个符号）:
  - `func NormalizeNetwork(network string) string`
  - `func NetworkType(network string) (string, bool)` — 返回 `UTXOCoinType`/`EvmCoinTye`/`EcdsaCoinType`/`Ed25519CoinType`/`TrxCoinType`/`BethCoinType`/`StarkCoinType`/`EOSCoinType` 之一
  - `func NetworkAddressType(network string) string`
  - `func MsgHeaderForNetwork(network string) string`
  - `var PorNetworkTypeMap map[string]string`（归一化 key）

- [ ] **Step 1: 写失败测试 `common/network_test.go`**

```go
package common

import "testing"

func TestNetworkTypeSamples(t *testing.T) {
	cases := []struct{ network, want string }{
		{"ETH", EvmCoinTye}, {"eth-arbitrum", EvmCoinTye}, {"ARBITRUM", EvmCoinTye},
		{"XLAYER", EvmCoinTye}, {"FEVM", EvmCoinTye}, {"ETC", EvmCoinTye},
		{"BTC", UTXOCoinType}, {"BCHN", UTXOCoinType}, {"ZCASH", UTXOCoinType},
		{"TRON", TrxCoinType}, {"TRX", TrxCoinType},
		{"OKC", EcdsaCoinType}, {"OKT", EcdsaCoinType}, {"AELF", EcdsaCoinType},
		{"XRP", EcdsaCoinType}, {"FIL", EcdsaCoinType},
		{"APTOS", Ed25519CoinType}, {"SOL", Ed25519CoinType}, {"TON", Ed25519CoinType},
		{"ASSET_HUB", Ed25519CoinType}, {"ASSET-HUB-KSM", Ed25519CoinType},
		{"LSK", Ed25519CoinType}, {"ADA", Ed25519CoinType},
		{"BETH", BethCoinType},
		{"STARKNET", StarkCoinType}, {"STARK", StarkCoinType},
		{"Vaulta", EOSCoinType}, {"WAXP", EOSCoinType},
	}
	for _, c := range cases {
		got, ok := NetworkType(c.network)
		if !ok || got != c.want {
			t.Errorf("NetworkType(%q) = %q, %v; want %q", c.network, got, ok, c.want)
		}
	}
	if _, ok := NetworkType("NO_SUCH_NETWORK"); ok {
		t.Error("NetworkType(NO_SUCH_NETWORK) should not be found")
	}
}

func TestNetworkAddressTypeSamples(t *testing.T) {
	cases := []struct{ network, want string }{
		{"ETH", "ETH"}, {"ARBITRUM", "ETH"}, {"XLAYER", "ETH"},
		{"FEVM", "FIL"}, {"FIL-EVM", "FIL"}, {"LAT", "LAT"},
		{"OKC", "ETH"}, {"OKT", "ETH"}, {"AELF", "ELF"},
		{"ASSET_HUB", "DOT"}, {"ASSET-HUB-KSM", "KSM"}, {"TONCOIN-NEW", "TON"},
		{"SOL", "SOL"}, {"ADA", "ADA"}, {"APTOS", "APTOS"},
		{"BCHN", "BCH"}, {"XEC", "BCH"}, {"ZCASH", "ZEC"}, {"BCHSV", "BTC"},
		{"HARMONY-ONE", "ONE"}, {"LUNA_TERRA", "TERRA"}, {"HEDERA", "HBAR"},
	}
	for _, c := range cases {
		if got := NetworkAddressType(c.network); got != c.want {
			t.Errorf("NetworkAddressType(%q) = %q; want %q", c.network, got, c.want)
		}
	}
}

func TestMsgHeaderForNetworkSamples(t *testing.T) {
	cases := []struct{ network, want string }{
		{"ETH", EthMessageSignatureHeader}, {"ARBITRUM", EthMessageSignatureHeader},
		{"BETH", EthMessageSignatureHeader},
		{"TRON", TronMessageSignatureHeader}, {"TRX", TronMessageSignatureHeader},
		{"BTC", BtcMessageSignatureHeader}, {"LTC", LtcMessageSignatureHeader},
		{"DOGE", DogeMessageSignatureHeader}, {"ZCASH", ZecMessageSignatureHeader},
		{"XEC", BtcMessageSignatureHeader}, {"ZEN", BtcMessageSignatureHeader},
		{"SOL", OKXMessageSignatureHeader}, {"OKC", OKXMessageSignatureHeader},
		{"LSK", OKXMessageSignatureHeader},
		// 未知 network：德勤流程走 ECDSA 兜底，消息头也要落到 OKX
		{"NO_SUCH_NETWORK", OKXMessageSignatureHeader},
	}
	for _, c := range cases {
		if got := MsgHeaderForNetwork(c.network); got != c.want {
			t.Errorf("MsgHeaderForNetwork(%q) = %q; want %q", c.network, got, c.want)
		}
	}
}

// TestNetworkTablesMatchLegacy cross-checks the new network tables against the
// legacy coin-keyed maps for every legacy key that survives as a network key.
// Deleted together with the legacy maps in the final task.
func TestNetworkTablesMatchLegacy(t *testing.T) {
	// ASSET-HUB: legacy addr type "ASSET-HUB" and "DOT" both resolve to ss58
	// network 0; the new table settles on "DOT".
	addrSkip := map[string]bool{"ASSET-HUB": true}

	for k, want := range PorCoinTypeMap {
		if got, ok := PorNetworkTypeMap[NormalizeNetwork(k)]; ok && got != want {
			t.Errorf("scheme mismatch for legacy key %s: new %q, legacy %q", k, got, want)
		}
	}
	for k, want := range PorCoinMessageSignatureHeaderMap {
		if _, ok := PorNetworkTypeMap[NormalizeNetwork(k)]; ok {
			if got := MsgHeaderForNetwork(k); got != want {
				t.Errorf("header mismatch for legacy key %s: new %q, legacy %q", k, got, want)
			}
		}
	}
	for k, want := range PorCoinAddressTypeMap {
		if addrSkip[k] {
			continue
		}
		if _, ok := PorNetworkTypeMap[NormalizeNetwork(k)]; ok {
			if got := NetworkAddressType(k); got != want {
				t.Errorf("addr type mismatch for legacy key %s: new %q, legacy %q", k, got, want)
			}
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./common/ -run 'TestNetwork|TestMsgHeader' -v`
Expected: FAIL，编译错误 `undefined: NetworkType` 等。

- [ ] **Step 3: 实现 `common/network.go`**

表的构造规则（已对 20 万行月度文件 + 两个德勤文件的词表核对过）：
- key = 月度 Network 词表 ∪ 德勤 network 词表 ∪ 旧表中的链名 key（去掉 token 变体），归一化后合并；
- 德勤词表中旧 `PorCoinTypeMap` 查不到的链（ARK、AVAX、BABYLON_BBN、CKB、DORA_FACTORY、DYDX_MAIN、INJECTIVE、MANTRA、SEI、UMEE_NEO）**故意不入表**——德勤流程对它们的现状就是 ECDSA 兜底，必须保持。

```go
package common

import "strings"

// NormalizeNetwork returns the canonical table key for a CSV network value:
// upper-case, "-" folded to "_" (monthly reports write ETH_LINEA, Deloitte
// reports write ETH-LINEA).
func NormalizeNetwork(network string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(network), "-", "_"))
}

// NetworkType resolves the signature-verification scheme for a network.
func NetworkType(network string) (string, bool) {
	t, ok := PorNetworkTypeMap[NormalizeNetwork(network)]
	return t, ok
}

// NetworkAddressType resolves the address-derivation type consumed by the
// Verify* functions. All EVM networks share ETH-style addresses; everything
// else defaults to the network name itself.
func NetworkAddressType(network string) string {
	n := NormalizeNetwork(network)
	if t, ok := networkAddressTypeExceptions[n]; ok {
		return t
	}
	if PorNetworkTypeMap[n] == EvmCoinTye {
		return "ETH"
	}
	return n
}

// MsgHeaderForNetwork derives the message signature header from the
// network's verification scheme. Unknown networks fall through to the OKX
// header, matching the Deloitte flow's default-ECDSA handling.
func MsgHeaderForNetwork(network string) string {
	n := NormalizeNetwork(network)
	switch PorNetworkTypeMap[n] {
	case EvmCoinTye, BethCoinType:
		return EthMessageSignatureHeader
	case TrxCoinType:
		return TronMessageSignatureHeader
	case UTXOCoinType:
		if h, ok := utxoMsgHeaderExceptions[n]; ok {
			return h
		}
		return BtcMessageSignatureHeader
	default:
		return OKXMessageSignatureHeader
	}
}

var (
	// PorNetworkTypeMap maps a normalized network name to its verification
	// scheme. Keys cover the monthly report Network column, the Deloitte
	// report network column, and chain-name keys inherited from the legacy
	// coin-keyed maps. Deloitte networks that the legacy maps never knew
	// (ARK, AVAX, CKB, ...) are intentionally absent: the Deloitte flow
	// falls back to ECDSA-with-pubkey for them.
	PorNetworkTypeMap = map[string]string{
		// UTXO
		"BTC": UTXOCoinType, "BCH": UTXOCoinType, "BCHN": UTXOCoinType,
		"BCHA": UTXOCoinType, "BSV": UTXOCoinType, "BCHSV": UTXOCoinType,
		"LTC": UTXOCoinType, "DOGE": UTXOCoinType, "DASH": UTXOCoinType,
		"BTG": UTXOCoinType, "BCD": UTXOCoinType, "DGB": UTXOCoinType,
		"QTUM": UTXOCoinType, "RVN": UTXOCoinType, "ZEC": UTXOCoinType,
		"ZCASH": UTXOCoinType, "XEC": UTXOCoinType, "ZEN": UTXOCoinType,
		"DCR": UTXOCoinType,

		// EVM
		"ETH": EvmCoinTye, "ETC": EvmCoinTye, "ETHW": EvmCoinTye, "ETHF": EvmCoinTye,
		"ARBITRUM": EvmCoinTye, "ETH_ARBITRUM": EvmCoinTye, "ARBITRUM_NOVA": EvmCoinTye,
		"OPTIMISM": EvmCoinTye, "ETH_OPTIMISM": EvmCoinTye,
		"BASE": EvmCoinTye, "ETH_BASE": EvmCoinTye, "ETH_LINEA": EvmCoinTye,
		"ZKSYNC2": EvmCoinTye, "ETH_ZKSYNC2": EvmCoinTye, "ETH_ZKSYNC": EvmCoinTye,
		"POLYGON": EvmCoinTye, "POLYGON_ZKEVM": EvmCoinTye,
		"XLAYER": EvmCoinTye, "OKB_X1": EvmCoinTye, "OKB_X1_TEST": EvmCoinTye,
		"AVAXC": EvmCoinTye, "BSC": EvmCoinTye, "BNB_BSC": EvmCoinTye, "OP_BNB": EvmCoinTye,
		"CFX_EVM": EvmCoinTye, "STORY": EvmCoinTye, "IP_STORY": EvmCoinTye,
		"MERLIN_BTC": EvmCoinTye, "MERLIN": EvmCoinTye,
		"CHZ": EvmCoinTye, "CHILIZ": EvmCoinTye,
		"RONIN": EvmCoinTye, "RON": EvmCoinTye,
		"KAIA": EvmCoinTye, "KLAY": EvmCoinTye,
		"ACE": EvmCoinTye, "ACE_MAIN": EvmCoinTye,
		"MOONBEAM": EvmCoinTye, "MOVR": EvmCoinTye,
		"METIS": EvmCoinTye, "METIS_MAINNET": EvmCoinTye,
		"FLR": EvmCoinTye, "FLARE": EvmCoinTye,
		"SCROLL": EvmCoinTye, "THETA": EvmCoinTye, "TFUEL": EvmCoinTye,
		"SONIC": EvmCoinTye, "S_SONIC": EvmCoinTye,
		"CELO": EvmCoinTye, "G": EvmCoinTye, "G_GRAVITY": EvmCoinTye, "GLRM": EvmCoinTye,
		"SOPHON": EvmCoinTye, "OASYS": EvmCoinTye, "OAS": EvmCoinTye, "ZETA": EvmCoinTye,
		"POLY_OPERA": EvmCoinTye, "FTM_OPERA": EvmCoinTye, "CRONOS": EvmCoinTye,
		"MXC_MXC": EvmCoinTye, "FIL_EVM": EvmCoinTye, "FEVM": EvmCoinTye,
		"XPL": EvmCoinTye, "SEI_EVM": EvmCoinTye, "MONAD": EvmCoinTye,
		"HYPEREVM": EvmCoinTye, "GUSDT_STABLE": EvmCoinTye, "ETH_KATANA": EvmCoinTye,
		"FRAX_EVM": EvmCoinTye, "ABSTRACT": EvmCoinTye, "CTXC": EvmCoinTye,
		"ETH_WORLD": EvmCoinTye, "SENTIENT": EvmCoinTye, "ETH_MEGA": EvmCoinTye,
		"PLS": EvmCoinTye, "HT_HECO": EvmCoinTye, "ETH_BLAST": EvmCoinTye,
		"APE_APECHAIN": EvmCoinTye, "MANTA2": EvmCoinTye, "TAIKO": EvmCoinTye,
		"KCC": EvmCoinTye, "ISLM": EvmCoinTye, "HOO": EvmCoinTye,
		"FITFI_STEP": EvmCoinTye, "FAN": EvmCoinTye, "ASTR_ASTAREVM": EvmCoinTye,
		"YOU": EvmCoinTye, "XETA": EvmCoinTye, "WTC": EvmCoinTye,
		"WEMIX3": EvmCoinTye, "TRUE": EvmCoinTye, "OMN": EvmCoinTye,
		"LINKEYE": EvmCoinTye, "INT_NEW": EvmCoinTye, "FSN": EvmCoinTye,
		"EVMOS": EvmCoinTye, "CMT": EvmCoinTye, "AAC_NEW": EvmCoinTye,
		"ETH_MODE": EvmCoinTye, "STC_EVM": EvmCoinTye, "OVER": EvmCoinTye,
		"DYM": EvmCoinTye, "DYM_TEST": EvmCoinTye, "WIN": EvmCoinTye,
		"VNT": EvmCoinTye, "LTK": EvmCoinTye, "IOTX": EvmCoinTye, "LAT": EvmCoinTye,
		"ETH_LSK": EvmCoinTye, "CORE": EvmCoinTye, "BERA": EvmCoinTye,
		"UNICHAIN": EvmCoinTye, "ETH_UNICHAIN": EvmCoinTye,

		// BETH
		"BETH": BethCoinType,

		// TRX
		"TRX": TrxCoinType, "TRON": TrxCoinType,

		// ECDSA
		"FIL": EcdsaCoinType, "CFX": EcdsaCoinType,
		"ELF": EcdsaCoinType, "AELF": EcdsaCoinType,
		"LUNC": EcdsaCoinType, "LUNA_TERRA": EcdsaCoinType, "TERRA": EcdsaCoinType,
		"OKT": EcdsaCoinType, "OKC": EcdsaCoinType,
		"XRP": EcdsaCoinType, "RIPPLE": EcdsaCoinType, "NULS": EcdsaCoinType,
		"STX": EcdsaCoinType, "TIA": EcdsaCoinType, "ATOM": EcdsaCoinType,
		"CRO": EcdsaCoinType, "DORA": EcdsaCoinType, "DYDX": EcdsaCoinType,
		"INJ": EcdsaCoinType, "ICX": EcdsaCoinType,
		"ONE": EcdsaCoinType, "HARMONY_ONE": EcdsaCoinType, "FLOW": EcdsaCoinType,

		// ED25519
		"SOL": Ed25519CoinType, "FOGO": Ed25519CoinType,
		"APTOS": Ed25519CoinType, "SUI": Ed25519CoinType,
		"TON": Ed25519CoinType, "TONCOIN_NEW": Ed25519CoinType,
		"VENOM": Ed25519CoinType,
		"DOT": Ed25519CoinType, "ASSET_HUB": Ed25519CoinType,
		"ASSET_HUB_KSM": Ed25519CoinType, "KSM": Ed25519CoinType,
		"ENJIN": Ed25519CoinType, "ENJ": Ed25519CoinType,
		"PHA_KHA": Ed25519CoinType, "PHALA": Ed25519CoinType,
		"CLV": Ed25519CoinType, "AVAIL": Ed25519CoinType, "SDN": Ed25519CoinType,
		"CFG_DOT": Ed25519CoinType, "EFI_DOT": Ed25519CoinType, "KARU": Ed25519CoinType,
		"XLM": Ed25519CoinType, "PI": Ed25519CoinType, "STELLAR": Ed25519CoinType,
		"ADA": Ed25519CoinType, "NEAR": Ed25519CoinType,
		"HBAR": Ed25519CoinType, "HEDERA": Ed25519CoinType,
		"ICP": Ed25519CoinType, "ACA": Ed25519CoinType, "CSPR": Ed25519CoinType,
		"SC": Ed25519CoinType, "IOTA": Ed25519CoinType,
		"IOTA_STARDUST": Ed25519CoinType, "IOTA_NEW": Ed25519CoinType,
		"TEZOS": Ed25519CoinType, "XTZ": Ed25519CoinType,
		"KDA": Ed25519CoinType, "EGLD": Ed25519CoinType,
		"ALGO": Ed25519CoinType, "ASTR": Ed25519CoinType, "IOST": Ed25519CoinType,
		"ERD": Ed25519CoinType, "CC_CANTON": Ed25519CoinType,
		"KLY": Ed25519CoinType, "POKT": Ed25519CoinType, "LSK": Ed25519CoinType,

		// STARK
		"STARKNET": StarkCoinType, "STARKNET_ETH": StarkCoinType, "STARK": StarkCoinType,

		// EOS
		"EOS": EOSCoinType, "A": EOSCoinType, "VAULTA": EOSCoinType,
		"WAX": EOSCoinType, "WAXP": EOSCoinType,
	}

	// utxoMsgHeaderExceptions lists UTXO networks whose message header is not
	// the Bitcoin one.
	utxoMsgHeaderExceptions = map[string]string{
		"LTC": LtcMessageSignatureHeader, "DOGE": DogeMessageSignatureHeader,
		"DASH": DashMessageSignatureHeader, "BTG": BtgMessageSignatureHeader,
		"BCD": BcdMessageSignatureHeader, "DGB": DgbMessageSignatureHeader,
		"QTUM": QtumMessageSignatureHeader, "RVN": RvnMessageSignatureHeader,
		"ZEC": ZecMessageSignatureHeader, "ZCASH": ZecMessageSignatureHeader,
	}

	// networkAddressTypeExceptions lists networks whose address-derivation
	// type differs from both the network name and the EVM default.
	networkAddressTypeExceptions = map[string]string{
		"BCHN": "BCH", "XEC": "BCH", "ZCASH": "ZEC",
		"BSV": "BTC", "BCD": "BTC", "BCHSV": "BTC",
		"FIL_EVM": "FIL", "FEVM": "FIL", "LAT": "LAT",
		"OKC": "ETH", "OKT": "ETH",
		"AELF": "ELF",
		"TRON": "TRX",
		"TONCOIN_NEW": "TON",
		"ASSET_HUB": "DOT", "ASSET_HUB_KSM": "KSM",
		"PHA_KHA": "PHA", "PHALA": "PHA",
		"CFG_DOT": "CFG", "EFI_DOT": "EFI",
		"HEDERA": "HBAR",
		"LUNA_TERRA": "TERRA",
		"HARMONY_ONE": "ONE",
		"A": "EOS", "VAULTA": "EOS", "WAX": "EOS", "WAXP": "EOS",
	}
)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./common/ -run 'TestNetwork|TestMsgHeader' -v`
Expected: 4 个测试全部 PASS。`TestNetworkTablesMatchLegacy` 若报 mismatch，以旧表为准修 network.go（旧表是行为基准）。

- [ ] **Step 5: Commit**

```bash
git add common/network.go common/network_test.go
git commit -m "feat(common): add network-keyed verification tables and resolvers"
```

---

### Task 3: `common/verify.go` 切换到 network 查表

**Files:**
- Modify: `common/verify.go`

**Interfaces:**
- Consumes: Task 2 的 `MsgHeaderForNetwork`、`NetworkAddressType`。
- Produces: `VerifyEvmCoin(network, addr, msg, sign string) error` 等——导出名和参数个数不变，第一个参数语义从 coin 变为 network。调用方传月度 CSV 的 Network 列值或德勤 network 列值。

- [ ] **Step 1: 逐函数替换查表**

每处都是同型改动：删除 map 查找 + `!exist` 报错，换成推导函数；参数 `coin` 改名 `network`；**错误信息格式串保持原样**（`%s` 处传 network）。共 8 个函数：

`VerifyBETH`（原 36-40 行）：
```go
func VerifyBETH(addr, msg, sign string) error {
	msgHeader := MsgHeaderForNetwork("BETH")
	hash := HashEvmCoinTypeMsg(msgHeader, msg)
	// ...其余不动
```

`UtxoCoinSigToPubKey`（原 93-97 行）：
```go
func UtxoCoinSigToPubKey(network, msg, sign string) ([]byte, error) {
	msgHeader := MsgHeaderForNetwork(network)
	// ...其余不动，函数体内 coin 变量名全部改为 network
```

`VerifyUtxoCoin`：签名改为 `func VerifyUtxoCoin(network, addr, msg, sign1, sign2, script string) error`，体内 `UtxoCoinSigToPubKey(coin, ...)` → `UtxoCoinSigToPubKey(network, ...)`，`VerifyUtxoCoinSig(coin, ...)` → `VerifyUtxoCoinSig(network, ...)`。

`VerifyUtxoCoinSig`（原 175-177 行）：
```go
func VerifyUtxoCoinSig(network, addr, script string, pub1, pub2 []byte) error {
	mainNetParams := &chaincfg.Params{}
	coinAddressType := NetworkAddressType(network)
	// ...switch 及其余不动，错误信息里的变量改传 network
```

`VerifyEvmCoin`（原 296-300、311-314 行）：
```go
func VerifyEvmCoin(network, addr, msg, sign string) error {
	msgHeader := MsgHeaderForNetwork(network)
	hash := HashEvmCoinTypeMsg(msgHeader, msg)
	s := MustDecode(sign)
	pub, err := sigToPub(hash, s)
	if err != nil {
		return ErrInvalidAddr
	}

	pubToEcdsa := pub.ToECDSA()
	recoverAddr := PubkeyToAddress(*pubToEcdsa).String()

	addrType := NetworkAddressType(network)
	switch addrType {
	// ...switch 体不动
```

`VerifyEd25519Coin`（原 347-351、359-362 行）：同型——`msgHeader := MsgHeaderForNetwork(network)`、`addrType := NetworkAddressType(network)`，删两处 `!exist` 分支。注意 348 行头查找失败原本直接返回错误，现在头永远可解析，行为差异仅出现在"旧表没有该 coin"的行，而这些行在月度流程本来到不了这里（先被 coin 表拦截）。

`VerifyEcdsaCoin`（原 461-465、476-479 行）：同型。

`VerifyStarkCoin`、`VerifyEOSCoin`：仅参数改名 `coin` → `network`（无查表）。

- [ ] **Step 2: 编译 + 跑 common 单测**

Run: `go build ./... && go test ./common/ -run 'TestNetwork|TestMsgHeader' -v`
Expected: 编译通过，测试 PASS。

**注意：本任务提交后、Task 5 完成前，月度流程处于已知的中间态**——main.go 仍传 coin 值（如 `USDT-ERC20`）进 `Verify*`，而这些值查不到 network 表会拿到错误的消息头，token 变体行会验签失败。这是预期的，不要在 Task 3/4 之后跑月度二进制回归；月度回归只在 Task 5 冒烟和 Task 7 全量做。德勤流程不受影响（它传的本来就是 network 值）。

- [ ] **Step 3: Commit**

```bash
git add common/verify.go
git commit -m "refactor(verify): resolve msg header and address type from network"
```

---

### Task 4: 德勤流程换表（`common/csv_verify.go`）

**Files:**
- Modify: `common/csv_verify.go:13,24,43`

**Interfaces:**
- Consumes: `NetworkType`。
- 注意：此文件各函数的 `coin` 参数实际传入的就是 network 列值（日志里都写 `network:%s`），只换查表，不改名不改日志。

- [ ] **Step 1: 三处 `PorCoinTypeMap[coin]` → `NetworkType(coin)`**

13 行、24 行：
```go
	coinType, exists := NetworkType(coin)
```
43-47 行（保留 ECDSA 兜底）：
```go
	coinType, exists := NetworkType(coin)
	if !exists {
		// If coin is not in mapping table, try using default ECDSA verification
		coinType = EcdsaCoinType
	}
```

- [ ] **Step 2: 跑德勤回归对比**

```bash
GOLD=/private/tmp/claude-502/-Users-oker-meili-zhiwei-li-dacs-at-okg-com-117-Downloads-code-proof-of-reserves/9b960432-1518-4034-90c7-6e7c96ba0b31/scratchpad/por-regression
POR_DELOITTE_CSV=example/德勤POR签名_DTT_OKC_AA26.csv go test ./common/ -run TestDeloitteRegression -v 2>&1 | grep '^REG ' | diff "$GOLD/deloitte-aa26.baseline.txt" -
POR_DELOITTE_CSV="example/dq-por Sheet1.csv" go test ./common/ -run TestDeloitteRegression -v 2>&1 | grep '^REG ' | diff "$GOLD/deloitte-dqpor.baseline.txt" -
POR_DELOITTE_CSV=example/test.csv go test ./common/ -run TestDeloitteRegression -v 2>&1 | grep '^REG ' | diff "$GOLD/deloitte-test.baseline.txt" -
```

Expected: 三个 diff 全部为空。若有差异：停下，逐 network 对照旧表定位 network.go 的表项错误，修正后重跑，不允许"看起来合理就放行"。

- [ ] **Step 3: Commit**

```bash
git add common/csv_verify.go
git commit -m "refactor(csv_verify): look up verification scheme by network table"
```

---

### Task 5: 月度流程按 Network 分发（`cmd/verifyaddress/main.go`）

**Files:**
- Modify: `cmd/verifyaddress/main.go`

**Interfaces:**
- Consumes: `common.NetworkType`；Task 3 后的 `common.Verify*`（第一参传 network）。

- [ ] **Step 1: `handle()` 读取 network 并改分发**

81 行附近，提取列时加上 network（`as[1+off]`）：
```go
	coin, network, addr, balance, message, sign1, sign2, script := as[0], as[1+off], as[3+off], as[4+off], as[5+off], as[6+off], as[7+off], as[8+off]
```

108-119 行，unit 查不到改为回退 coin 本身（删除整行失败逻辑）：
```go
	coin = strings.ToUpper(coin)
	totalCoin := coin
	if u, exist := common.PorCoinUnitMap[coin]; exist {
		totalCoin = u
	}
	if _, exist := coinTotalBalance[totalCoin]; exist {
		coinTotalBalance[totalCoin] = coinTotalBalance[totalCoin].Add(val)
	} else {
		coinTotalBalance[totalCoin] = val
	}
```

130 行 switch 改为：
```go
	scheme, _ := common.NetworkType(network)
	switch scheme {
```
switch 体内所有 `common.VerifyEvmCoin(coin, ...)` / `VerifyEcdsaCoin(coin, ...)` / `VerifyEd25519Coin(coin, ...)` / `VerifyUtxoCoin(coin, ...)` / `VerifyStarkCoin(coin, ...)` 的第一参改传 `network`（`VerifyTRX`/`VerifyBETH` 无此参数，不动）。

default 分支报 network：
```go
	default:
		fmt.Println(fmt.Sprintf("Fail to verify address %s signature. Invaild coin type:%s", addr, network))
		return coin, false
```
（格式串原样保留，包括原有的 "Invaild" 拼写，值从 coin 换成 network。）

其余不动：panic recover、余额解析、`IsVerifyAddressBannedCoin(coin)`、成功/失败按 coin 计数。

- [ ] **Step 2: 编译并冒烟**

```bash
GOLD=/private/tmp/claude-502/-Users-oker-meili-zhiwei-li-dacs-at-okg-com-117-Downloads-code-proof-of-reserves/9b960432-1518-4034-90c7-6e7c96ba0b31/scratchpad/por-regression
go build -o "$GOLD/verifyaddress-new" ./cmd/verifyaddress
"$GOLD/verifyaddress-new" --por_csv_filename example/okx_por_2026070700_V6.csv | tail -20
```

Expected: 正常跑完，输出 per-coin 统计。

- [ ] **Step 3: Commit**

```bash
git add cmd/verifyaddress/main.go
git commit -m "feat(verifyaddress): dispatch signature verification by Network column"
```

---

### Task 6: 删除三张旧表和过渡测试

**Files:**
- Modify: `common/coin.go`（删 `PorCoinTypeMap`、`PorCoinAddressTypeMap`、`PorCoinMessageSignatureHeaderMap` 三个变量，约 900 行；consts、`PorCoinUnitMap`、`PorCoinBaseUnitPrecisionMap`、两个 BlackList、两个 `Is*BannedCoin` 函数保留）
- Modify: `common/network_test.go`（删 `TestNetworkTablesMatchLegacy`——它引用旧表，使命已完成）

- [ ] **Step 1: 删除并确认无残留引用**

Run: `grep -rn "PorCoinTypeMap\|PorCoinAddressTypeMap\|PorCoinMessageSignatureHeaderMap" --include="*.go" .`
Expected: 无输出。

- [ ] **Step 2: 编译 + 全量 common 单测**

Run: `go build ./... && go test ./common/ -run 'TestNetwork|TestMsgHeader' -v`
Expected: 编译通过，测试 PASS。

- [ ] **Step 3: Commit**

```bash
git add common/coin.go common/network_test.go
git commit -m "refactor(coin): drop legacy coin-keyed verification maps"
```

---

### Task 7: 端到端回归 + 零配置验收

**Files:**
- 无代码改动（验证任务）；如发现表项错误则回到 network.go 修复并补提交。

- [ ] **Step 1: 月度二进制回归**

```bash
GOLD=/private/tmp/claude-502/-Users-oker-meili-zhiwei-li-dacs-at-okg-com-117-Downloads-code-proof-of-reserves/9b960432-1518-4034-90c7-6e7c96ba0b31/scratchpad/por-regression
go build -o "$GOLD/verifyaddress-new" ./cmd/verifyaddress
"$GOLD/verifyaddress-new" --por_csv_filename example/okx_por_2026070700.csv | sort > "$GOLD/monthly-11col.new.txt"
"$GOLD/verifyaddress-new" --por_csv_filename example/okx_por_2026070700_V6.csv | sort > "$GOLD/monthly-12col.new.txt"
diff "$GOLD/monthly-11col.baseline.txt" "$GOLD/monthly-11col.new.txt"
diff "$GOLD/monthly-12col.baseline.txt" "$GOLD/monthly-12col.new.txt"
```

Expected: diff 为空。若不为空，允许的差异只有两类，其余一律定位修复：
1. 失败行错误信息里打印的标识从 coin 名变成 network 名（仅当基线中本来就有失败行）；
2. 不允许有第 2 类。` accoounts, `（per-coin 统计）行和 `Total balance:` 行必须逐字节一致。

- [ ] **Step 2: 德勤回归复跑**

重复 Task 4 Step 2 的三条 diff 命令。
Expected: 全部为空（Task 6 删表后语义不得再变）。

- [ ] **Step 3: 零配置验收——虚构新 token 应直接通过**

从月度文件抄一行真实 ETH 行，把 coin 名改成不存在的 token（签名对消息签的，与 coin 名无关，所以仍有效）：

```bash
ROW=$(awk -F, 'NR>26 && $1=="ETH" && $2=="ETH" {print; exit}' example/okx_por_2026070700.csv)
MINI="$GOLD/newtoken.csv"
printf 'coin,amount\nNEWTOKEN-XYZ,1\n\ncoin,Network,Snapshot Height,address,amount,message,signature1,signature2,redeem script/ public key,EOA1,EOA2\n' > "$MINI"
echo "$ROW" | sed 's/^ETH,/NEWTOKEN-XYZ,/' >> "$MINI"
"$GOLD/verifyaddress-new" --por_csv_filename "$MINI"
"$GOLD/verifyaddress-baseline" --por_csv_filename "$MINI"
```

Expected: 新二进制输出 `NEWTOKEN-XYZ 1 accoounts, 1 verified, 0 failed` 且 `all address passed`；基线二进制对同一文件报 `invalid coin name`——证明"新 token 零配置"达成。

- [ ] **Step 4: 收尾提交（如有修复）**

若 Step 1-3 触发了 network.go 修复，逐一提交；全绿后汇报结果（含三类证据：月度 diff、德勤 diff、NEWTOKEN 验收输出）。

---

## 已知风险（执行时留意）

- BETH / LSK / STARKNET 在样本文件里没有数据行，回归覆盖不到，表项按旧语义保留（见 spec"已知盲点"）。
- 若老月度文件存在 Network 单元格为空而 coin 有值的行，旧逻辑能过、新逻辑会失败——金样 diff 会暴露，届时再决策（预期样本中不存在）。
- `TestVerifyCSVFileMultithread` 引用缺失的 `merged - Sheet1.csv`、`TestVerifyCSVFileStarknetOnly` 会真跑 test.csv——全程用 `-run` 过滤，不跑全量 `go test ./common/`。
