# 發布 Tunlease

這份 runbook 提供給維護者。一般使用者只需要 README 的安裝說明。

## 發布 release

1. 從乾淨且 CI 通過的 `main` branch 開始。
2. 選擇如 `v0.2.0` 的 semantic version。
3. 確認所有 user-facing 文件及其成對的 `*.zh-TW.md` 內容一致。
4. 建立並推送 annotated tag：

   ```bash
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin v0.2.0
   ```

Tag workflow 會驗證版本、執行測試與 lint、將 multi-architecture gateway
image 發布到 GHCR，並建立包含 CLI binaries 與 SHA-256 files 的 GitHub
Release。

驗證已完成的 release：

```bash
gh run list --limit 5
gh release view v0.2.0
docker pull ghcr.io/iml885203/tunlease-gateway:v0.2.0
```

不要移動或取代已發布的 version tag；請發布新的 patch release。

## 更新 Homebrew tap

Tap 每六小時檢查最新 GitHub Release，並建立 formula update pull request。
Review macOS 與 Linux test run 後再 merge。

若 automation 無法使用，請在 GitHub Release 成功後手動更新：

1. 開啟由 Homebrew 管理的 tap checkout：

   ```bash
   brew tap iml885203/tap
   cd "$(brew --repository iml885203/tap)"
   git pull --ff-only
   ```

2. 計算 immutable source archive 的 checksum，且不在 tap checkout 留下檔案：

   ```bash
   curl -fsSL \
     https://github.com/iml885203/tunlease/archive/refs/tags/v0.2.0.tar.gz |
     shasum -a 256
   ```

3. 更新 `Formula/tunlease.rb` 的 `url` 與 `sha256`。
4. 在同一個 tap checkout 執行：

   ```bash
   brew style --formula iml885203/tap/tunlease
   brew audit --strict --new --online iml885203/tap/tunlease
   brew uninstall --force tunlease 2>/dev/null || true
   HOMEBREW_NO_INSTALL_FROM_API=1 \
     brew install --build-from-source iml885203/tap/tunlease
   brew test iml885203/tap/tunlease
   ```

5. 推送 tap 變更，並確認其 macOS 與 Linux workflow 通過。

Tap 會從 source build，不依賴預先編譯的 release binaries。

## 更新 Scoop bucket

`iml885203/scoop-bucket` repository 每六小時檢查最新 release 並建立
manifest update pull request。其 Windows workflow 會透過 Scoop 安裝 manifest
並執行 CLI；請在 merge 前 review 該次 run。

Manifest 使用 release workflow 發布的 Windows binary 與 SHA-256 file。
若 automation 無法使用，須一起更新 `version`、download URL 與 hash。

## Homebrew Core readiness

Formula 在技術上已為 Homebrew Core 做好準備：

- 公開且使用 MIT license 的 source；
- stable semantic-version tags 與 immutable source archives；
- 使用 build-only Go dependency 從 source build；
- 會啟動 gateway 並檢查 health endpoint 的功能性測試；
- 沒有 self-update command；
- tap 具備 macOS 與 Linux formula CI。

向 `Homebrew/homebrew-core` 提案前，應重新確認最新的
[formula requirements](https://docs.brew.sh/Acceptable-Formulae)、
[package acceptance policy](https://docs.brew.sh/Package-Acceptance-Policy)
與 [contribution process](https://docs.brew.sh/How-To-Open-a-Homebrew-Pull-Request)。
Homebrew 通常不接受未滿 30 天的專案，也通常不接受缺乏足夠 notability
的作者自行提交專案。這些是 adoption gates，不是 Tunlease 缺少的功能；
在專案符合條件前，持續以 tap 作為支援的安裝方式。
