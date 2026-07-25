# 發布 Tunlease

[English](releasing.md) · [繁體中文](releasing.zh-TW.md)

這份 runbook 提供給 maintainer。一般使用者只需要 README 的安裝說明。

## 發布

1. 確認 `main` 包含預定變更，以及成對的英文與繁中文件。
2. 在 GitHub Actions 從 `main` 執行 **Release**。
3. 選擇 `patch`、`minor` 或 `major`。

檢查通過後，workflow 會建立版本，並自動處理 GitHub Release、gateway
images、Helm defaults、Homebrew、Scoop 與 public relay deployment。

## 必要 secrets

將以下 fine-grained personal access tokens 設為 repository secrets：

| Secret | 限定 repository | Repository permissions |
|---|---|---|
| `HOMEBREW_TAP_TOKEN` | `iml885203/homebrew-tap` | `Actions: Read and write`、`Contents: Read` |
| `SCOOP_BUCKET_TOKEN` | `iml885203/scoop-bucket` | `Actions: Read and write`、`Contents: Read` |
| `PUBLIC_RELAY_INFRA_TOKEN` | `iml885203/tunlease-public-relay-infra` | `Actions: Read and write`、`Contents: Read` |

## 驗證或復原

**Release** workflow 全部呈現綠色即代表發布完成。可用以下指令檢查 artifacts：

```bash
VERSION=vX.Y.Z
gh release view "$VERSION"
docker pull "ghcr.io/iml885203/tunlease-gateway:$VERSION"
```

若只有一個 downstream step 失敗，可用既有的 `vX.Y.Z` 執行
**Sync Homebrew**、**Sync Scoop** 或 **Deploy public relay**，不需建立新版本。

不要移動或取代已發布的 version tag；請改為發布 patch release。
