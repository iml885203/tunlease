# 發布 Tunlease

這份 runbook 提供給維護者。一般使用者只需要 README 的安裝說明。

## 發布 release

1. 確認 `main` 包含預定發布的變更，以及成對的英文與繁中文件。
2. 在 GitHub Actions 從 `main` 執行 **Release** workflow。
3. 選擇 `patch`、`minor` 或 `major`。

Workflow 會計算下一個 stable semantic version，並在建立 annotated tag
之前執行 formatting、vet、build、race tests、固定版本 lint 與 Helm
驗證。接著才會將 multi-architecture gateway images 發布到 GHCR，並建立
包含 CLI binaries 與 SHA-256 files 的 GitHub Release。Preflight
失敗時不會建立 version tag。

手動推送合法的 annotated tag 仍可作為復原方式；tag CI 會使用同一份
reusable artifact workflow。

驗證已完成的 release：

```bash
VERSION=vX.Y.Z # 使用剛發布的版本
gh run list --limit 5
gh release view "$VERSION"
docker pull "ghcr.io/iml885203/tunlease-gateway:$VERSION"
```

不要移動或取代已發布的 version tag；請發布新的 patch release。

## 更新 Homebrew tap

Tap 每六小時檢查最新 GitHub Release，並建立 formula update pull request。
另一個 workflow 只會在 macOS 與 Linux tests 都通過後 merge 該 PR。

若 automation 無法使用，請在 GitHub Release 成功後手動更新：

1. 開啟由 Homebrew 管理的 tap checkout：

   ```bash
   brew tap iml885203/tap
   cd "$(brew --repository iml885203/tap)"
   git pull --ff-only
   ```

2. 計算 immutable source archive 的 checksum，且不在 tap checkout 留下檔案：

   ```bash
   VERSION=vX.Y.Z # 使用剛發布的版本
   curl -fsSL \
     "https://github.com/iml885203/tunlease/archive/refs/tags/$VERSION.tar.gz" |
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
manifest update pull request。其 Windows workflow 會透過 Scoop 安裝
manifest 並執行 CLI；另一個 workflow 只會在該 test 成功後 merge PR。

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
