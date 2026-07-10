# Teams差分同期に最終成功時刻と24時間オーバーラップを導入する

Status: polished
Model: unknown
Created: 2026-07-10
Updated: 2026-07-10
Branch: feat/20260710-teams-overlap-sync

## 概要

Teamsのチャネル・チャット同期を、コンテナ単位の最終成功時刻から24時間重複して取得する差分同期へ変更する。

## 背景

`config.example.yaml` と `internal/config` には `initial_lookback_days` と `overlap_duration` が存在するが、`internal/sync` は全件取得を行い、`sync_states` を利用していない。Graphのチャット一覧は `lastModifiedDateTime` の範囲フィルターを利用できるが、チャネル一覧は追加のODataクエリに対応しないため、取得順序を利用した停止判定が必要である。

参考: [Chat messages](https://learn.microsoft.com/en-us/graph/api/chat-list-messages?view=graph-rest-1.0)、[Channel messages](https://learn.microsoft.com/en-us/graph/api/channel-list-messages?view=graph-rest-1.0)

## 問題

通常同期のたびに既存メッセージを全件取得しており、API呼び出しとページング量が増える。また、編集・遅延反映されたメッセージを取りこぼす境界時刻の保証がない。

## 目標

初回は設定された遡及期間から取得し、以後は前回の成功時刻から24時間前を下限として再取得する。重複取得はUPSERTで安全に吸収し、失敗した同期は次回の取得開始時刻を進めない。

## 対象外

- Graph Change Notificationsの導入
- 完全再同期CLIの追加
- Mail/Calendar同期
- 検索仕様の変更

## 提案する方針

- `sync_states(resource_type, resource_id)` をチャネル・チャットごとの状態源として扱い、取得開始時刻、最終成功時刻、最終エラー、連続失敗数をStore APIで読み書きする。
- 同期開始時刻をUTCで取得し、状態がない場合は `started_at - initial_lookback_days`、状態がある場合は `last_success_at - overlap_duration` を下限にする。
- Chatは `lastModifiedDateTime desc` と同じプロパティの `gt` フィルターを付ける。
- Channelはレスポンスが更新日時降順であることを利用し、スレッド全体の最終更新時刻が下限より古くなったページ以降を停止する。`replies@odata.nextLink` が返る場合は200件超の返信も取得する。
- 全ページとUPSERTが完了した場合のみ、同期開始時刻を `last_success_at` に保存する。失敗時は `last_attempt_at` とエラーだけを保存する。
- `.gitignore` の `teams-knowledge` パターンをルートバイナリに限定し、`cmd/teams-knowledge/main.go` をGit管理対象にする。

## 受け入れ条件

- [ ] 状態がないコンテナは `now - initial_lookback_days` から取得する。
- [ ] 状態があるコンテナは `last_success_at - overlap_duration` から取得する。
- [ ] 同期成功後、開始時刻が最終成功時刻として保存される。
- [ ] ページ取得またはUPSERTが失敗した場合、最終成功時刻が更新されない。
- [ ] 24時間以内に編集されたメッセージと返信が再取得され、UPSERT後に検索結果が更新される。
- [ ] 削除メッセージは既存のTombstone規則で検索対象から除外される。
- [ ] `cmd/teams-knowledge/main.go` が `git status` の未追跡・無視対象にならない。

## テスト計画

- `internal/sync` のモックGraphを使い、初回・通常・境界時刻・失敗後再実行を単体テストする。
- 複数ページ、チャネル返信の追加ページ、重複ページ、削除メッセージをSQLite結合テストする。
- `go test ./...` を実行する。
- 実環境では小規模な1チャネルと1チャットで、編集後24時間以内の再取得と状態更新を確認する。

## リスク

チャネル一覧はフィルター非対応のため、更新日時順の保証が変わった場合は停止判定が不完全になる。同期開始時刻を成功時刻に使わないと実行中に発生した更新を取りこぼすため、成功時刻は必ず開始時刻として保存する。

## 変更履歴

`CHANGES.md` impact: yes

項目案:

- Teams同期がコンテナ単位の最終成功時刻と24時間オーバーラップを利用するようになった。

## 注記

実装開始前に、初回コミット前の既存ファイルをベースラインとして確定する。
