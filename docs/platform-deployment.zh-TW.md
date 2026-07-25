# 平台部署

[English](platform-deployment.md) · [繁體中文](platform-deployment.zh-TW.md)

## 支援的 topology

把既有 callback host 的 `/` 路由到單一 gateway replica，再把
`fail_open_url` 指向原 app 的 internal origin。這是取代 host 原本的 `/`
route，不是新增一個互相競爭的 Ingress。

Rollout 前確認：

- origin URL 不會繞回 gateway；
- app 不使用 `/_tunlease`；
- Ingress 支援 WebSocket upgrade 與長 idle timeout；
- public host 有可信 TLS；
- 所有 unclaimed path 仍由 origin 正常處理。

沒有 HTTP(S) origin 時 gateway 會拒絕啟動。只支援一個 replica；active
session 位於 process memory。

## Helm

Chart 預設使用已發布的 gateway image。建立 private values，設定既有 host、
原 app、允許的 path 與可選 token：

```yaml
ingress:
  host: callbacks.staging.example.com
  path: /
config:
  failOpenURL: http://myapp-origin.default.svc
  maxClaims: 64
  whitelist:
    - /webhooks/provider/
auth:
  tokens:
    - owner: alice
      token: ALICE_TOKEN
```

```bash
helm upgrade --install tunlease charts/tunlease \
  --namespace tunlease --create-namespace \
  -f values.private.yaml
```

只有使用自己的 build 或 release mirror 時才覆寫 `image.repository` 與
`image.tag`。

Gateway YAML 只有 `listen`、必填 `fail_open_url`、`max_claims`、`whitelist`
與 `tokens`。未知欄位會讓啟動失敗，避免舊設定被靜默忽略。`/_tunlease`
與單一 replica 是固定條件，不是 values。

空 whitelist 允許所有合法 path。空 tokens 會停用認證；所有 client 共用
`anonymous` owner，可互相 list 或 release tunnel。可信網路外應為每位 owner
設定獨立 secret token。

## 驗證與操作

```bash
curl -fsS https://callbacks.staging.example.com/_tunlease/healthz
curl -fsS https://callbacks.staging.example.com/webhooks/provider/example
kubectl -n tunlease logs deployment/tunlease-gateway --since=10m
```

驗證完整順序：unclaimed request 到 origin；執行 `tunle claim` 後到
localhost；Ctrl+C 後同一 URL 回到 origin。`healthz` 只證明 process 能回 HTTP。

Rollout 會斷開所有 client；新 gateway 可連線後 client 會重連，空窗期間 request
走 origin。請使用 recreate 類型 rollout，或確保永遠只有一個 routable replica。
Rollback 也必須還原 host 原本的 `/` route。

HTTPS/WSS 保護 client 到 TLS terminator；若 TLS 在 Pod 前終止，cluster hop
位於 trust boundary 內，不可信時應使用 re-encryption 或 mTLS。Dispatch 到
tunnel 後的失敗不會 replay 到 origin，因為 localhost 可能已處理 request。
