# 發布 Tunlease

這份 runbook 提供給維護者。一般使用者只需要 README 的安裝說明。

## 發布 release

1. 確認 `main` 包含預定發布的變更，以及成對的英文與繁中文件。
2. 在 GitHub Actions 從 `main` 執行 **Release** workflow。
3. 選擇 `patch`、`minor` 或 `major`。

Workflow 會計算下一個 stable semantic version，並在建立 annotated tag
之前執行 formatting、vet、build、race tests、固定版本 lint 與 Helm
驗證。接著才會將 multi-architecture gateway images 發布到 GHCR，並建立
包含 CLI binaries 與 SHA-256 files 的 GitHub Release。發布完成後會平行
dispatch Homebrew tap 與 Scoop bucket updaters、等待各自的 tests 與 merge，
並確認兩個 package definitions 都符合新版本。Release 的最後一步會將對應的
immutable gateway image dispatch 到 public relay infrastructure，並等待該次
部署及 health check 成功。Preflight 失敗時不會建立 version tag。

發布前需設定 `HOMEBREW_TAP_TOKEN` repository secret。請使用只限
`iml885203/homebrew-tap` 的 fine-grained personal access token，repository
permissions 只需 `Actions: Read and write` 與 `Contents: Read`。Tap updater
會使用自身 repository-scoped `GITHUB_TOKEN` 更新及 merge formula；跨 repo
token 只負責 dispatch 與觀察該 workflow。
可用既有 release version 手動執行 **Sync Homebrew** workflow，以驗證 token
或在不發布另一個 release 的情況下復原。

`SCOOP_BUCKET_TOKEN` repository secret 也採用相同設定，但只限
`iml885203/scoop-bucket`，repository permissions 為
`Actions: Read and write` 與 `Contents: Read`。也可用既有 release version
手動執行 **Sync Scoop** workflow，以進行驗證或復原。

請設定 `PUBLIC_RELAY_INFRA_TOKEN` repository secret，使用只限
`iml885203/tunlease-public-relay-infra` 的 fine-grained personal access
token。Repository permissions 需為 `Actions: Read` 與
`Contents: Read and write`：Contents write 用於送出已驗證的
`repository_dispatch`，Actions read 則讓 release workflow 等待並確認該次
部署。Infrastructure workflow 會拒絕來源 repository 或 stable semantic
version payload 不符合預期的 dispatch。

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

Release workflow 會在 artifacts 發布後立即 dispatch tap updater；tap 仍每六
小時檢查最新 GitHub Release 作為 fallback。Updater 會建立 formula update pull
request、等待 macOS 與 Linux tests，且只在兩者都通過後 merge 該 PR。

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

Release workflow 會在 artifacts 發布後立即 dispatch
`iml885203/scoop-bucket` updater；bucket 仍每六小時檢查最新 release 作為
fallback。其 Windows workflow 會透過 Scoop 安裝 manifest 並執行 CLI；
updater 會等待，且只在該 test 成功後 merge PR。接著 release workflow
會確認 merged manifest 符合新版本。

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
