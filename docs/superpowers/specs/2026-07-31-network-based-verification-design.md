# 设计：验签方案改由 Network 列驱动

日期：2026-07-31
分支：feature/ada-bip322-address-verification（实现时另开分支）

## 背景与问题

当前验签流程（`cmd/verifyaddress/main.go` 与德勤审计流程 `common/csv_verify.go`）
以 CSV 第 1 列 coin 名为 key 查 `common/coin.go` 里的三张手工映射表：

- `PorCoinTypeMap`（coin → 验签方案）
- `PorCoinAddressTypeMap`（coin → 地址类型）
- `PorCoinMessageSignatureHeaderMap`（coin → 消息签名头）

三张表合计约 900 行。每上一个新 token（哪怕是已支持链上的 ERC-20）都要在
3~4 处手动加映射，而 CSV 中已有的 Network 列（`as[1+off]`）解析后完全没有使用。

对 `okx_por_2026070700.csv`（20 万行）的统计确认：Network → 验签方案是
无例外的一对一关系；26 个 network 覆盖全部 60+ 种 coin。德勤流程传入查表
函数的"coin"参数实际已是 network 列的值，现有 map 中混入的 "TRON"、
"STELLAR"、"HEDERA" 等链名 key 即为此产生。

## 目标

- 验签方案（算法、地址类型、消息头）由 Network 列决定。
- 新 token 上线零配置；新链上线只加一行。
- 存量 POR 报告文件（月度 + 德勤）验证结果不变。

## 非目标

- `cmd/checkbalance` 与 `common/address.go` 不改（它们只依赖
  `PorCoinUnitMap`、`PorCoinBaseUnitPrecisionMap`、`PorCoinDataMap`，
  不引用三张验签表）。
- 不引入外部配置文件。

## 词表现状（两条流程不一致）

- 月度文件 Network 列：`TRON`、`OKC`、`ARBITRUM`、`ASSET_HUB`（下划线风格），26 个取值。
- 德勤文件 network 列：`TRX`、`OKT`、`ETH-ARBITRUM`、`ASSET-HUB`（连字符风格），
  约 130 个取值，含 ARK、CKB、MANTRA 等目前靠 ECDSA 默认兜底的链。

## 设计

### 1. 数据表（`common/coin.go` 验签部分重写）

- 删除 `PorCoinTypeMap`、`PorCoinAddressTypeMap`、`PorCoinMessageSignatureHeaderMap`。
- 新增 `PorNetworkTypeMap`（network → 验签方案）：
  - key 归一化：`strings.ToUpper` 后将 `-` 与 `_` 归一（存储形式统一用 `_`），
    查表前对输入做同样归一化。`ASSET-HUB`/`ASSET_HUB` 等各留一条。
  - 内容 = 月度词表(26) ∪ 德勤词表(~130)，去重后约 140 行。
- 消息头改为按方案推导，函数 `MsgHeaderForNetwork(network)`：
  - EVM、BETH → ETH 头（`\x19Ethereum Signed Message:\n32`）
  - TRX → TRON 头
  - UTXO → 按链查小表（LTC/DOGE/DASH/BTG/DGB/QTUM/RVN/ZEC 各自消息头，
    其余 UTXO 链默认 BTC 头；ZEN/XEC/DCR/BCHSV/BCD 现状即 BTC 头）
  - 其他方案（ECDSA/ED25519/STARK/EOS/ALGO）→ OKX 头
  - 已核对现有 300 行头表，无违反此规律的例外。
- 新增 `NetworkAddressTypeMap`（network → 地址类型），只存例外：
  EVM 各 L2/侧链 → ETH、FEVM/FIL_EVM → FIL、AELF → ELF、ASSET_HUB → DOT、
  ASSET_HUB_KSM → KSM、OKC/OKT → ETH 等，约 15 条；
  查不到默认取 network 本身（SOL→SOL、ADA→ADA 等恒等占多数）。
- 保留不动：`PorCoinUnitMap`、`PorCoinBaseUnitPrecisionMap`、
  `CheckBalanceCoinBlackList`、`VerifyAddressCoinBlackList`（均仍以 coin 为 key）。

### 2. `common/verify.go`

- `VerifyEvmCoin` / `VerifyEcdsaCoin` / `VerifyEd25519Coin` / `VerifyUtxoCoin` /
  `VerifyStarkCoin` 等函数的 `coin` 参数改名为 `network`，内部查表换为
  `MsgHeaderForNetwork` 与 `NetworkAddressTypeMap`。
- 函数签名的参数个数不变，调用方语义变化见下。

### 3. `cmd/verifyaddress/main.go`（月度流程）

- `handle()` 读取 `network := as[1+off]`，以 `PorNetworkTypeMap[normalize(network)]`
  做 switch 分发；verify 函数传 network。
- coin 列仅用于：
  - 余额汇总单位：`PorCoinUnitMap` 查不到时不再整行失败，默认 unit = coin 本身
    （代价：coin 名打错不会再被拦截，由用户确认接受）。
  - 黑名单：`IsVerifyAddressBannedCoin` 仍按 coin（EOS/A/RIPPLE/USDT-ALGO 行为不变）。
- unknown network → 该行失败，错误信息打印 network 名。

### 4. `common/csv_verify.go`（德勤流程）

- 调用点不动（传入的本来就是 network 值），查表换成新表。
- unknown network → 默认 ECDSA 的兜底保留。

## 错误处理

- 月度流程：network 不在表中 → 行失败并报 network 名（比现在报 coin 名更准确）。
- 德勤流程：维持现状（unknown → ECDSA 兜底，验签失败自然报错）。

## 回归验证

- 用改造前后两个二进制分别跑 `example/` 下全部月度格式文件
  （含 `okx_por_2026070700.csv` 20 万行、9/11/12 列老格式），输出 diff 必须一致；
  唯一预期差异：unknown coin 不再因 unit 缺失而失败。
- 德勤流程：`go test` 跑 `csv_verify_test.go` 对
  `德勤POR签名_DTT_OKC_AA26.csv` 与 `dq-por Sheet1.csv`，通过/失败行数与改造前一致。

## 已知盲点

BETH、LSK、STARKNET 在手头样本文件中没有数据行，无法回归验证。按现有语义
保留对应 network key：BETH → BETH 方案、LSK → Ed25519 + OKX 头、
STARKNET/STARK → Stark。需在下一份真实报告中人工确认这三者 Network 列的实际写法。

## 安全性说明

验签方案由 CSV 声明的 Network 列选择不弱化审计保证：任何方案下签名都必须
密码学地对应到该行声明的地址/公钥，声明错误的 network 只会导致验证失败，
不存在"选一个容易通过的方案"的空间。
