# 程式碼慣例

[English](code-conventions.md) · [繁體中文](code-conventions.zh-TW.md)

這些慣例用來引導 Tunlease 的結構調整，補充 [`AGENTS.md`](../AGENTS.md)
中的 architecture invariants，以及 [`architecture.zh-TW.md`](architecture.zh-TW.md)
記載的 observable contracts。優先選擇能清楚表達 ownership 與 behaviour 的
最小設計。

## 依 domain 組織

檔名應回答「這個檔案擁有哪個概念？」，而不是「這是哪一層？」。使用
`claim.go`、`release.go`、`tunnel_session.go` 或 `tunnel_proxy.go` 等
domain 名稱，不建立 `utils.go`、`helpers.go` 或 `services.go` 這類
包山包海的檔案。

多個操作共用程式碼時，以共用概念命名檔案。例如 claim、list 與 release
共用的程式碼，應放在以 claim 或 lifecycle 命名的檔案，而不是任意塞進其中
一個 command 檔案。Package entry-point 與 `_test.go` 檔案不受此限制。

拆分大型檔案本身，不構成新增 package、interface 或 layer 的理由。除非存在
真正的 ownership boundary，否則 cohesive code 應留在目前的 package。

## 保留結構化錯誤

增加 context 時使用 `%w` wrap error。使用 `errors.Is` 或 `errors.As` 分類
error；production code 不得以比對 error 字串來決定 control flow。

偵測狀況的 domain 擁有自己的 error vocabulary。Caller 需要辨識狀況時，
使用 sentinel 或 typed error。User-facing layer 可以將這些 error 轉換為
穩定的 API code 或可採取行動的 CLI message，但不可丟失 unwrap chain。

Tests 在驗證 user-facing message 時可以檢查 error text。若 caller 已提供所有
有用 context，直接回傳 error 也可以；不要加入重複且沒有資訊量的 wrapping。

## 將重複操作放回 owning domain

相同且 non-trivial 的多步驟操作出現在至少三個 production call sites 時，
在擁有該操作的 type 或 package 上提供 entry point。相關 sentinel errors 與
behavioural policy 也由該 domain 擁有。

不要只因為一段程式碼能被命名就抽出 entry point：

- 只有一個 production caller 的 function 留在 caller 附近。
- Standard library 外面只有一到三行的 trivial wrapper 直接 inline。
- Retry policy、timeout、output sink 或 failure semantics 刻意不同的操作，
  不要強行合併。

三個 call sites 是 design signal，不是硬性命令。若表面相似掩蓋不同的 domain
behaviour，保留 duplication 會更清楚。

## 有意識地選擇 callback 與 interface

同一 package 內的簡單信號傳遞，優先使用 function-field callback，不要只為了
替 layer 命名而建立 thin interface。Output 或 activity notification 等
optional observational hook 適合使用 callback。

下列情況使用 interface：

- Caller 需要在 tests 中替換 implementation；
- Implementation 位於另一個 package；
- 多個 implementations 確實具有不同 behaviour；或
- `http.Handler`、`io.Reader`、`net.Conn` 等既有 Go interface 已能表達
  contract。

Interface 應保持 narrow，並從 consumer 的需求定義。不要只為了減少 type
數量，就把 `registry.Store` 等既有 cross-package boundary 改成 callback。

## 記錄 channel delivery policy

每個 exported event channel 或 subscription API，都要記錄 delivery 是
blocking、buffered、best-effort 或 lossless。使用 non-blocking send 時，
必須明確說明 slow consumer 可能遺失 event。

用於 terminal 顯示的 request activity 等 observational event 可以是
best-effort。Terminal release 與 expiry 等 correctness-critical signal，
不可共用允許 drop 的 channel。

Channel ownership 也必須清楚：producer 關閉自己的 output channel；consumer
不可關閉不是自己建立的 channel。

## 封裝 mutable shared state

Mutable shared state 應屬於擁有 lock 的 receiver type。避免 package-level
mutable map 與 slice。Mutex 與 fields 的保護關係不明顯時，要用 comment
說明。

不要暴露需要 caller 持有 internal lock 才能安全修改的 mutable state。若
caller 可能在 lock 釋放後修改內部狀態，回傳 map、slice 或含 pointer value
時應提供 snapshot 或 clone。

Locked region 應集中在 state transition。Network call、stream I/O、
callback 或其他可能 blocking 的操作期間，不要持有 mutex。

## 重構驗證

除非 change 明確另有說明，refactor 必須保持 observable behaviour。編輯期間
先跑最窄的相關 tests，handoff 前執行：

```bash
make preflight
make e2e
git diff --check
```

若 refactor 改變 user-facing semantics，同一個 change 必須同步更新英文與配對
的繁體中文文件。
