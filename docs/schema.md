# Firestore スキーマ

papers は Firestore（Native mode）を本番のリポジトリとして使う。このドキュメントは
コレクション構成、トランザクション、リアルタイム配信、インデックスと TTL の設計をまとめる。
実装は `pkg/repository/firestore/`、契約テストは `pkg/repository/*_test.go`。

## 設計方針

1. **ペーパー全体のカウンタを持たない。** Firestore の 1 ドキュメントへの持続的な書き込みは
   毎秒 1 回程度が目安。paper ドキュメントを commit ごとに更新すると、人と AI が同時に
   書くペーパーでボトルネックになる。そこで履歴の単位をブロックにした。
   - 各ブロックが自分の `Version` を持ち、変更のたびに `versions/{n}` にスナップショットを残す
     （= 「ブロックの commit ログが paper をつくる」）。
   - commit はブロックのバージョン番号だけを参照する小さなレコード。順序は `CreatedAt`。
   - paper ドキュメントはタイトル変更時と、`UpdatedAt` が `--paper-touch-interval`（既定 1 分）
     より古いときだけ書く。
2. **変更イベントはストレージから取る。** アプリ側の pub/sub は持たず、各インスタンスが
   開いているペーパーの Firestore リスナー（paper / blocks / locks）を購読して WebSocket に流す。
   インスタンスをまたいだ配信が DB だけで完結し、Cloud Run のスケールアウトにそのまま乗る。
3. **イベントは「結果の状態」を運ぶ。** `block_put`（ブロック全体）/ `block_deleted` /
   `paper_updated` / `lock` / `unlock`。クライアントは `Version` を比べて冪等に適用するので、
   重複や順序の入れ替わりがあっても状態は収束する。
4. **モデルをそのまま保存する。** フィールド名は Go の構造体フィールド名（`ParentID` など）。
   ミラー構造体や `firestore:` タグは使わない（hecatoncheires の規約を踏襲）。

## コレクション

| パス | 内容 | 主なクエリ |
|---|---|---|
| `users/{userID}` | `model.User` | 全件（ユーザー選択用） |
| `emails/{base64url(lower(email))}` | `{UserID}`。メールの一意性インデックス | ID 直引き |
| `identities/{provider}/subjects/{subject}` | `model.Identity`（IdP の subject → user） | ID 直引き |
| `sessions/{sessionID}` | `model.Session`（秘密値はハッシュ） | ID 直引き。TTL: `ExpiresAt` |
| `tokens/{tokenID}` | `model.APIToken` | `UserID ==` |
| `groups/{groupID}` | `model.Group` | `Members array-contains` |
| `workspaces/{wsID}` | `model.Workspace`（RoleBinding を内包） | 全件（件数は少ない前提） |
| `workspaces/{ws}/papers/{paperID}` | `model.Paper`（アクセス設定を内包） | 全件 |
| `.../papers/{p}/blocks/{blockID}` | `model.Block`（現在の状態） | `ParentID ==` + `Order` 範囲・並び |
| `.../blocks/{b}/versions/{version}` | `model.Block` のスナップショット（不変） | `Version` 降順 |
| `.../papers/{p}/commits/{commitID}` | `model.Commit`（変更の要約） | `CreatedAt` 降順 + カーソル |
| `.../papers/{p}/locks/{blockID}` | `model.BlockLock` | 全件。TTL: `ExpiresAt` |

- 階層はサブコレクションで表す（`_` で意味を連結した名前は使わない）。
- ブロックを削除しても `versions` は残る。削除されたブロックの履歴を見たり、元に戻したりできる。
- ID はすべて UUIDv7（時刻順）。ブロック ID は UI が先に採番することもある（UUID のみ受け付ける）。

## トランザクション

### commit（`RunPaperTx`）

```
read  papers/{p}                         （共有ロック。書くのはタイトル変更 / touch 時のみ）
read  blocks/{id}                        （変更対象のブロック）
query blocks where ParentID==X and Order>Y order by Order limit k   （挿入位置の決定）
----- ここから書き込み（Firestore の制約で読み込みの後にまとめる）
set    blocks/{id}
create blocks/{id}/versions/{v}          （Create なので同じバージョンの二重書き込みは失敗する）
delete blocks/{id}
create commits/{c}
set    papers/{p}                        （必要なときだけ）
```

- Firestore のトランザクションは「読み込みが全部先、書き込みが後」。実装（`fsTx`）は書き込みを
  メモリに積み、読み込みはそれを上書きして見せ、最後に flush する。
- 1 トランザクションの書き込み上限（500）に収めるため、1 commit で変更できるブロックは
  `MaxChangesPerCommit = 240`（1 変更 = ブロック + バージョンの 2 書き込み）。
  表は `rows × cols ≤ 200` セル。
- 同じ位置への同時挿入は同じ `Order` キーになりうる。並びは `(Order, ID)` で決まり、
  次の挿入は「`Order` が厳密に大きい次の兄弟」との間にキーを作るので壊れない。
- ブロック単位の後勝ち: 同じブロックへの同時書き込みは Firestore のトランザクション競合で
  直列化され、後の書き込みが勝つ。古い `base_version` で書いた場合は `OverwroteVersion` を記録する。

### ロック

`locks/{blockID}` を読んで、他の保持者の有効なロックがあれば拒否、なければ書く（トランザクション）。
ハートビートは TTL の 1/3 ごと（既定 10 秒）に同じドキュメントを上書きする。期限切れのドキュメントは
読み込み側で無視し、物理削除は TTL ポリシーに任せる。

## リアルタイム配信（`WatchPaper`）

WebSocket 1 接続につき 3 つのリスナーを張る。

| リスナー | 変換 |
|---|---|
| `papers/{p}` ドキュメント | 更新 → `paper_updated`、削除 → `paper_deleted` |
| `blocks` コレクション | 追加・更新 → `block_put`、削除 → `block_deleted` |
| `locks` コレクション | 追加・更新 → `lock`、削除 → `unlock` |

- 3 つとも初回スナップショットを受け取ってから `WatchPaper` を返す。クライアントは `hello` を受けてから
  ペーパーを読み直すので、その間の変更を取りこぼさない（初回スナップショット自体は送らない）。
- 受け手が遅れてバッファ（`--event-buffer`）が溢れたら、チャネルを閉じて `closing: resync` を送る。
  クライアントは再接続して読み直す。
- 課金: リスナーは変更ごとに読み取り 1 回分。同じインスタンスで同じペーパーを複数人が開くと
  リスナーも人数分になる。規模が出たら、インスタンス内で共有する方式を検討する。

## インデックス

`deploy/firestore.indexes.json` に定義（Firebase CLI 形式）。

- `blocks`: `(ParentID ASC, Order ASC)` と `(ParentID ASC, Order DESC)`。挿入位置の決定と末尾の取得に使う。
- `blocks.Content` / `versions.Content` は単一フィールドインデックスを除外する（最大 256 KiB の
  本文をインデックスに入れない）。

gcloud で作る場合:

```sh
gcloud firestore indexes composite create --collection-group=blocks \
  --field-config=field-path=ParentID,order=ascending --field-config=field-path=Order,order=ascending
gcloud firestore indexes composite create --collection-group=blocks \
  --field-config=field-path=ParentID,order=ascending --field-config=field-path=Order,order=descending
gcloud firestore indexes fields update Content --collection-group=blocks --disable-indexes
gcloud firestore indexes fields update Content --collection-group=versions --disable-indexes
```

## TTL ポリシー

```sh
gcloud firestore fields ttls update ExpiresAt --collection-group=sessions --enable-ttl
gcloud firestore fields ttls update ExpiresAt --collection-group=locks --enable-ttl
```

TTL による削除は最大 24 時間ほど遅れることがあるので、アプリは常に `ExpiresAt` を見て判定する。

## 既知の制約と今後

- **全文検索がない。** 現状の検索はブロックの全件走査で、ペーパー数に比例して読み取りが増える。
  次の段階で検索用の索引（Vertex AI Search / ベクトル検索、または別ストア）を足す。
- **ワークスペース一覧は全件読み。** ワークスペースが数百を超えるなら、メンバーシップの逆引き
  ドキュメントを持つ。
- **ペーパー削除は非アトミック。** paper ドキュメントを先に消し（読み手からは即座に消える）、
  子コレクションを BulkWriter で掃除する。途中で失敗したら残骸は見えないまま残るので、
  掃除ジョブを後で用意する。

## テスト

```sh
gcloud emulators firestore start --host-port=127.0.0.1:8085 &
FIRESTORE_EMULATOR_HOST=127.0.0.1:8085 TEST_FIRESTORE_PROJECT_ID=papers-test go test ./pkg/repository/...
```

`task test:firestore` は Docker でエミュレータを立てて同じことを行う。
