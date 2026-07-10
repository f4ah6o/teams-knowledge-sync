# Outlook Calendarを同期ウィンドウ単位delta同期する

Status: polished
Model: unknown
Created: 2026-07-10
Updated: 2026-07-10
Branch: feat/20260710-calendar-delta-sync

## 概要

予定表と半開区間の同期ウィンドウごとにcalendarView deltaリンクを保存し、追加・更新・削除・未来範囲の延長を定期同期する。

## 背景

calendarView deltaは固定した開始・終了日時の範囲ごとに状態トークンを管理する必要があり、初回は完全同期、その後はdeltaリンクで増分取得する。[Microsoft Graph event delta](https://learn.microsoft.com/en-us/graph/api/event-delta?view=graph-rest-1.0)

## 問題

予定表全期間を毎回取得するとページング量が大きく、定期予定の変更、例外、キャンセル、削除を効率よく追跡できない。

## 目標

予定表・ウィンドウ単位でdelta同期し、各ウィンドウの障害やtoken失効を分離しながら過去・未来の範囲を維持する。

## 対象外

- 予定の書き込み、出席回答、空き時間検索
- Calendar MCP
- Mail delta

## 提案する方針

- `calendar_sync_windows(calendar_id, window_start_utc, window_end_utc, next_link, delta_link, last_attempt_at, last_success_at, last_error, consecutive_failures)` を主状態にする。
- ウィンドウは半開区間 `[start,end)` とし、既定で過去3か月、直近未来1か月、遠い未来3か月に分割する。
- 初回は各ウィンドウのcalendarView deltaを完全取得し、全ページ成功後にdeltaLinkを保存する。次回は保存済みURL全体を再利用する。
- `@removed` を削除またはキャンセルとして反映し、範囲外イベントの削除通知も対象ウィンドウの整合性を壊さないよう判定する。
- token失効時は該当ウィンドウだけ再初期化し、未来の同期済み範囲が設定値を下回ったら新規ウィンドウを追加する。
- Daemonはウィンドウ単位でエラーを記録し、他の予定表・ウィンドウを継続する。

## 受け入れ条件

- [ ] 予定表とウィンドウの組み合わせごとにdelta状態が独立して保存される。
- [ ] `[start,end)` の境界でイベントを二重計上しない。
- [ ] nextLinkの全ページを処理した後だけdeltaLinkが更新される。
- [ ] 追加・更新・削除・キャンセル・定期予定例外がSQLiteへ反映される。
- [ ] token失効時に該当ウィンドウだけ完全同期へ戻る。
- [ ] 未来の同期範囲が設定値を下回った場合、新しいウィンドウが作成される。
- [ ] 1ウィンドウの429・権限エラーが他ウィンドウを停止させない。

## テスト計画

- ウィンドウ生成、境界、deltaLink遷移、token失効復旧の単体テストを行う。
- 複数ページ、削除、キャンセル、定期予定変更、重複UPSERTのSQLite結合テストを行う。
- Graphモックで429、401、403、nextLink/deltaLinkを検証する。
- `go test ./...` を実行する。

## リスク

calendarView deltaは開始・終了日時を初回クエリへ固定し、後続はtoken URLをそのまま再利用する必要がある。範囲外の削除通知が返るため、対象ウィンドウの範囲判定を行ってからローカル状態を変更する。

## 変更履歴

`CHANGES.md` impact: yes

項目案:

- Outlook Calendarがウィンドウ単位のdelta同期と未来範囲の延長に対応した。

## 注記

Calendar初回同期イシューの完了を前提とする。
