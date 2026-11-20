# papers 設計メモ

最終更新: 2026-11-20 / 初期実装時点

## コンセプト

- **paper** = 人間と AI が共存して知識を積み上げるキャンバス。Wiki / Notion に近いが、主な書き手は AI（API / MCP）で、人間は読む・直す・指示する側。
- **block** = 編集とロックの単位。Markdown 相当（見出し・段落・リスト・ToDo・引用・コールアウト・コード・区切り線・画像・表）に加え、AI だけが書く **リッチブロック**（`mermaid` / `svg` / `html`）を持つ。人間の UI はリッチブロックを描画するだけで編集手段を持たない。
- **commit** = 複数 op のアトミックな適用。paper のリビジョン = 最新 commit の `seq`。変更前後のブロックのスナップショットを残すので、履歴の閲覧と将来の revert ができる。
- **workspace** で paper を束ね、UI で切り替える。

## データモデル

```
Workspace ─┬─ bindings: [RoleBinding]          (subject → role)
           └─ Paper ─┬─ access: {restricted, bindings}
                     ├─ Block (parent_id, order, type, content, props, version)
                     ├─ Commit (seq, actor, message, changes[before/after])
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
| 配信 | commit / lock / unlock / access_changed を EventBus でペーパー購読者へ配信。クライアントは `seq` の飛びを検知したら再取得 |

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
- MCP（Streamable HTTP, stateless）: `papers_guide` / `papers_list_workspaces` / `papers_list_papers` / `papers_search` / `papers_read_paper` / `papers_create_paper` / `papers_commit` / `papers_history`。
  `insert_markdown` op を使うと AI は Markdown 1 本で大量に書ける（```mermaid / ```svg / ```html フェンスはリッチブロック、GFM の表は表ブロックになる）。

## デプロイ: Cloud Run + LB と WebSocket

結論: **WebSocket は使える。ただし前提を設計に織り込む必要がある**（今の実装は織り込み済み）。

- Cloud Run は WebSocket をサポートし、外部 HTTPS LB（サーバーレス NEG）経由でも動く。
- 接続はリクエストタイムアウト（最大 60 分）で必ず切れる → `--timeout=3600` を設定し、クライアントは自動再接続＋再同期（実装済み：指数バックオフ、`hello` 受信時に再取得）。
- session affinity はベストエフォート → 別インスタンスに繋がっても問題ないよう、状態はすべて共有バックエンド（DB + EventBus）に置く。**インスタンス間のイベント配信手段が必須**（下記 DB 選定とセット）。
- WebSocket 接続中はインスタンスが課金対象（リクエスト中扱い）。`--concurrency` を大きめに、`--min-instances=1` 推奨。
- Origin 検証はライブラリ側で Host と一致を確認。LB 経由でも Host は保持される。

## リポジトリ（DB）選定 — 未決

インターフェース（`interfaces.Repository` / `PaperTx` / `EventBus`）は両方式で実装できる形にしてあり、現状はメモリ実装のみ。決める必要があるのは「どれを本番実装にするか」。

| 観点 | Firestore | AlloyDB（または Cloud SQL for PostgreSQL） |
|---|---|---|
| コスト | 従量課金・ゼロスケール。小〜中規模なら非常に安い | 常時起動。AlloyDB は最小構成でも月 $200 以上規模、Cloud SQL は小さいインスタンスならかなり安い |
| トランザクション | ペーパー単位の read-modify-write は可能。ただし **1 ドキュメントへの持続書き込みは約 1 回/秒が目安**。現設計の `paper.revision` カウンタはホットドキュメントになりうる | 行ロックで高頻度の commit も問題なし |
| リアルタイム配信 | **snapshot listener がそのまま EventBus になる**（各インスタンスが購読中ペーパーを listen） | `LISTEN/NOTIFY` で実装可能（インスタンスごとに常時接続が 1 本必要）。または Pub/Sub |
| 検索・AI 連携 | 全文検索なし。ベクトル検索はあり | 全文検索（tsvector）、pgvector / ScaNN でセマンティック検索。「AI が知識を探す」用途に強い |
| 一覧の認可フィルタ | アプリ側でフィルタ | 同上（RLS も選択肢） |
| 運用 | ほぼ不要、hecatoncheires と同じ流儀で実装可能 | マイグレーション管理・接続管理が必要 |

所感:
- **小さく始めて安く運用したい** → Firestore。ただし commit 頻度を抑えるため、ブロック単位のバージョンを主にして paper 全体のカウンタを避ける（commit の順序を ULID + ブロックバージョンで扱う）などの調整が必要。
- **「AI が知識を探して書く」ことを主機能に据える** → PostgreSQL 系。検索・ベクトル・集計がネイティブで、commit ログもリレーショナルに扱いやすい。最初は Cloud SQL、規模が出たら AlloyDB へ移行するのが現実的（ワイヤ互換）。
- 個人的な推奨は **PostgreSQL 系（Cloud SQL から開始）**。理由は検索・ベクトルが製品の中心機能になりそうなこと、commit のホットスポット問題がないこと、`LISTEN/NOTIFY` で配信も完結すること。

## 今後のタスク

1. 本番リポジトリ実装（上記の決定後）と、それに対応する EventBus 実装。`runRepositoryTest` に追加すれば既存の契約テストがそのまま走る。
2. 検索のインデックス化（現状は全走査）とセマンティック検索。
3. Rego による組織ポリシーのフック。
4. 履歴からの revert / 差分表示。
5. コメント・メンション、AI へのタスク依頼（ペーパー上で「ここを書いて」と指示する UX）。
6. ブロック内の共同編集（現状はブロック単位のロック。文字単位 CRDT は必要になってから）。
7. Terraform（Cloud Run + LB + DB + Secret Manager）。
