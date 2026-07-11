# Outlook Mailをフォルダー単位delta同期する

Status: done
Model: unknown
Created: 2026-07-10
Updated: 2026-07-11
Branch: claude/polished-issues-i4v4uh

## 概要

MailフォルダーごとにMicrosoft Graphのdeltaリンクを保存し、追加・更新・削除・移動を定期同期する。

## 背景

Mail deltaはフォルダー単位の同期で、ページ完了時に `@odata.deltaLink` を保存し、次回はそのURL全体を再利用する仕様である。[Microsoft Graph message delta](https://learn.microsoft.com/en-us/graph/api/message-delta?view=graph-rest-1.0)

## 問題

初回同期後も全メールを再取得すると負荷が高く、削除・移動・既読状態をローカルへ反映できない。

## 目標

各対象フォルダーを独立してdelta同期し、1フォルダーの障害が他フォルダーを停止させないようにする。

## 対象外

- Calendar delta
- Mail MCPと高度な検索
- 共有メールボックス

## 提案する方針

- `mail_sync_states(folder_id, next_link, delta_link, last_attempt_at, last_success_at, last_error, consecutive_failures)` を追加する。
- 初回は既存の初回取得処理からdelta状態を初期化し、以後は保存済みのnext/delta URLをそのままGraphへ渡す。
- 全ページの反映が成功して `@odata.deltaLink` を受け取った場合だけdeltaリンクをコミットする。
- `@removed` は本文・インデックスを削除してIDと削除時刻を保持し、移動は元フォルダーから削除されたものとして処理する。
- 429ではRetry-Afterと指数バックオフを使い、token失効時は該当フォルダーだけ初期同期へ戻す。
- フォルダー単位の失敗を記録し、Daemonは成功・失敗にかかわらず次のフォルダーへ進む。

## 受け入れ条件

- [x] フォルダーごとにdeltaリンクが独立して保存される。
- [x] nextLinkの全ページを処理し、完了後だけdeltaLinkが更新される。
- [x] 追加・更新・削除・移動をローカルへ反映できる。
- [x] 同じdeltaページを再処理しても重複しない。
- [x] 429、401/403、token失効、単一フォルダー失敗を記録し、他フォルダー同期を継続できる。
- [x] Daemonの定期実行で対象フォルダーを再同期できる。

## テスト計画

- nextLink/deltaLink遷移、再実行、token失効復旧の単体テストを行う。
- `@removed`、移動、重複、複数フォルダー分離のSQLite結合テストを行う。
- Graphモックで429のRetry-Afterとページングを検証する。
- `go test ./...` を実行する。

## リスク

delta URLは不透明な状態トークンであり、クエリを再構成せずURL全体を保存・再利用する必要がある。読み取り状態の変化がdeltaに含まれる場合もあるため、対象判定を削除イベントだけに限定しない。

## 変更履歴

`CHANGES.md` impact: yes

項目案:

- Outlook Mailがフォルダー単位のdelta同期と削除反映に対応した。

## 注記

初回同期イシューの完了を前提とする。

- 2026-07-11: `mail_sync_states`とフォルダー単位のdelta状態機械を実装。nextLinkを都度チェックポイントし、deltaLinkは全ページ反映後のみ確定。`@removed`はフォルダー一致ガード付きtombstoneで移動と両立。410/SyncStateNotFoundは該当フォルダーのみ再初期化。`Prefer: IdType="ImmutableId"`で移動後もIDを維持。429のRetry-Afterは`internal/graph`のhttptestで検証。`daemon`は`sync.interval`ごとに再同期。
