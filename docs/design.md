# papers 設計メモ

最終更新: 2026-11-21 / Firestore スキーマ反映

## コンセプト

- **paper** = 人間と AI が共存して知識を積み上げるキャンバス。Wiki / Notion に近いが、主な書き手は AI（API / MCP）で、人間は読む・直す・指示する側。
- **block** = 編集とロックの単位。Markdown 相当（見出し・段落・リスト・ToDo・引用・コールアウト・コード・区切り線・画像・表）に加え、AI だけが書く **リッチブロック**（`mermaid` / `svg` / `html`）を持つ。人間の UI はリッチブロックを描画するだけで編集手段を持たない。
- **commit** = 複数 op のアトミックな適用。各ブロックは自分のバージョン履歴（スナップショット）を持ち、commit は「どのブロックをどのバージョンからどのバージョンにしたか」を記録する。ペーパー全体のリビジョン番号は持たない。
- **workspace** で paper を束ね、UI で切り替える。

## データモデル

```
Workspace ─┬─ bindings: [RoleBinding]          (subject → role)
           └─ Paper ─┬─ access: {restricted, bindings}
                     ├─ Block (parent_id, order, type, content, props, version)
                     │    └─ versions/{n}  (snapshot per version, kept after delete)
                     ├─ Commit (actor, message, changes[block, from→to version])
                     └─ BlockLock (block_id, holder_key, holder, expires_at)
User ── Identity (provider, subject)   ※ user_id は papers が発行
Group (members)      APIToken (kind: personal|agent, scope: read|write)
```

- **順序**: 兄弟ブロックは fractional index（`model.KeyBetween`）で並べる。挿入・移動で他ブロックを書き換えない。
- **表**: `table` ブロックの子として `table_cell` ブロックを rows × cols 個持つ。セルごとに独立して編集・ロックできる（Google Spreadsheet のセルに近い）。表のサイズ変更でセルが自動生成・削除される。
- **Actor**: すべての書き込みに `{kind: human|agent, user_id, agent_name}` を記録。AI は必ず「誰かの代理」として動き、その人の権限をトークンのスコープで上限を切って借りる。

## 同時編集モデル

| 仕組み | 内容 |
|---|---|
| ソフトロック | ブロックをクリックすると WebSocket 経由でロック取得（TTL 30 秒、1/3 TTL ごとのハートビートで延長、切断で解放）。他者には「◯◯が編集中」と表示 |
| 書き込み制御 | 他人がロック中のブロックへの commit は `409 locked`。`force: true` で上書き可能（UI の「それでも編集する」、MCP は「ユーザーが明示したときのみ」とガイド） |
| 後勝ち | ロックを越えた書き込みは last-writer-wins。`base_version` が古いと `overwrote_version` を記録し、UI で通知・履歴に警告を表示 |
| 配信 | ブロック・ロック・ペーパーの変更を、ストレージの変更フィード（Firestore リスナー）から購読者へ配信。イベントは結果の状態を運び、クライアントはバージョン比較で冪等に適用する |

ロック保持者は「接続」単位（`holder_key` = WebSocket の connection id、API ならトークン ID）。同じユーザーの別タブは別の保持者として扱う。

## 認証・認可

- **認証**: Google OIDC（authorization code + nonce、ID トークンは JWKS で署名検証）。初回ログインで papers が `user_id`（UUIDv7）を発行し、`Identity(provider, subject)` で紐付ける。将来の別 IdP は `IdentityProvider` 実装を足すだけ。検証済みメールが一致する既存ユーザーには自動リンク。
- ログイン可否は `--allowed-domain` / `--allowed-email`（Google 利用時はどちらか必須）。`--admin-email` はシステム管理者。
- ブラウザはセッション Cookie（`HttpOnly`, `SameSite=Lax`、秘密値はハッシュで保存）。変更系リクエストは `X-Requested-With: papers` ヘッダ必須（CSRF 対策）。
- API / MCP は Bearer トークン（`pap_<id>.<secret>`）。MCP はブラウザ Cookie を受け付けない。トークンはブラウザセッションからのみ発行可能。
- **認可**（`model.Principal`）:
  - ロール: `viewer < editor < owner`。subject: user / group / email domain / 全サインインユーザー。
  - ワークスペースロール = bindings の最大値。システム管理者は全体の owner。
  - ペーパーロール: ワークスペース owner は全ペーパーの owner。作成者は（ワークスペースで editor 以上である限り）owner。非公開（restricted）ペーパーはワークスペースロールを無視し、ペーパー側の bindings のみ。公開ペーパーではペーパー側 bindings はロールを上げることだけできる。
  - トークン上限: read → viewer、write → editor。トークンは権限管理（共有設定変更・ワークスペース作成・グループ管理）を行えない。
  - 読めないものは 404、読めるが権限不足は 403（存在を漏らさない）。
- Rego について: 現状の要件はロールモデルで表現できるため Go 実装にした。組織固有のルール（例: 特定ラベルのペーパーは特定グループのみ、など）が出てきた時点で、`PaperRole` の結果を上書きする Rego フック（hecatoncheires と同じ opaq）を追加する想定。

## API / MCP

- REST: `/api/v1/...`（ワークスペース、ペーパー、`POST .../commits`、履歴、ロック、Markdown 書き出し、検索、トークン、グループ）
- WebSocket: `GET .../papers/{id}/live`（プロトコルは `pkg/controller/http/live.go` 冒頭のコメント）
- MCP（Streamable HTTP, stateless）: `papers_guide` / `papers_list_workspaces` / `papers_list_papers` / `papers_search` / `papers_read_paper` / `papers_create_paper` / `papers_commit` / `papers_history` / `papers_block_history`。
  `insert_markdown` op を使うと AI は Markdown 1 本で大量に書ける（```mermaid / ```svg / ```html フェンスはリッチブロック、GFM の表は表ブロックになる）。

## デプロイ: Cloud Run + LB と WebSocket

結論: **WebSocket は使える。ただし前提を設計に織り込む必要がある**（今の実装は織り込み済み）。

- Cloud Run は WebSocket をサポートし、外部 HTTPS LB（サーバーレス NEG）経由でも動く。
- 接続はリクエストタイムアウト（最大 60 分）で必ず切れる → `--timeout=3600` を設定し、クライアントは自動再接続＋再同期（実装済み：指数バックオフ、`hello` 受信時に再取得）。
- session affinity はベストエフォート → 別インスタンスに繋がっても問題ないよう、状態はすべて共有バックエンドに置く。インスタンス間のイベント配信は Firestore のリスナーで行う（下記）。
- WebSocket 接続中はインスタンスが課金対象（リクエスト中扱い）。`--concurrency` を大きめに、`--min-instances=1` 推奨。
- Origin 検証はライブラリ側で Host と一致を確認。LB 経由でも Host は保持される。

## リポジトリ（DB）: Firestore に決定

2026-11-21 決定。KVS（Firestore）を前提にスキーマを組んだ。詳細は [schema.md](schema.md)。

- 履歴の単位を「ブロック」にした。ブロックごとにバージョンとスナップショットを持ち、commit はそれを参照する。ペーパー全体のリビジョンカウンタは持たない（1 ドキュメントへの書き込み上限を避けるため）。
- リアルタイム配信は Firestore のリスナーで行い、アプリ側の pub/sub は持たない。
- 弱点は全文検索。これは別の仕組み（Vertex AI Search / ベクトル検索など）で補う。

比較の経緯: PostgreSQL 系は検索と高頻度の書き込みに強いが常時コストがかかる。Firestore はゼロスケールで安く、リスナーでインスタンス間の配信が完結する。ホットスポットの問題はブロック単位の履歴にすることで回避できると判断した。

## 今後のタスク

1. Firestore エミュレータで契約テストを回す（`task test:firestore`）。本番プロジェクトにインデックスと TTL を設定する。
2. 検索のインデックス化（現状は全走査）とセマンティック検索。
3. Rego による組織ポリシーのフック。
4. 履歴からの revert / 差分表示。
5. コメント・メンション、AI へのタスク依頼（ペーパー上で「ここを書いて」と指示する UX）。
6. ブロック内の共同編集（現状はブロック単位のロック。文字単位 CRDT は必要になってから）。
7. Terraform（Cloud Run + LB + DB + Secret Manager）。
