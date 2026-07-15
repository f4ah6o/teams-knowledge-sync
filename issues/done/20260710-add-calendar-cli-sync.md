# Outlook Calendar CLI初回同期を追加する

Status: done
Model: GPT-5
Created: 2026-07-10
Updated: 2026-07-15
Branch: claude/polished-issues-i4v4uh

## 概要

`outlook` にCalendar機能を追加し、選択した予定表の指定期間を初回取得してSQLiteから検索・表示できるようにする。

## 背景

`docs/initial-calendar-plan.md` は、既定予定表を初期対象とし、過去3年・未来1年、単発・定期・例外・キャンセル・Teams会議・非公開予定を対象としている。

## 問題

現在は予定表、イベント、タイムゾーン、参加者、会議URLを保存するモデルとCLIがない。

## 目標

予定表一覧と期間指定のcalendarView初回取得を実装し、イベントをUTC基準で保存して日付・期間検索と詳細表示を提供する。

## 対象外

- Calendar delta、同期ウィンドウ、Daemon
- 予定の作成・更新・削除・出席回答
- 空き時間検索、会議室予約、添付本体
- Microsoft 365グループ予定表

## 提案する方針

- Mailと同じ `outlook` バイナリ、認証、Graphクライアント、SQLiteを共有する。
- `calendar.calendars`、`calendar.range`、`display_timezone`、非公開予定の保存・公開設定を追加する。
- 予定表一覧を取得し、指定した `[from,to)` のcalendarViewをページングする。
- `calendars`、`calendar_events`、attendees、locations、categories、attachments、FTSを追加する。
- 開始・終了をUTCへ正規化し、元タイムゾーン、終日、定期種別、例外、キャンセル、Outlook URL、Teams会議URLを保存する。
- `calendar list/show/sync/day/range/search/status` を実装する。

## 受け入れ条件

- [x] 既定予定表を含む予定表一覧を取得し、同期対象を選択できる。
- [x] 指定期間のcalendarViewを複数ページ取得して重複なく保存できる。
- [x] 単発、定期発生、例外、終日、キャンセル、Teams会議を識別できる。
- [x] UTC、元タイムゾーン、主催者、出席者、場所、会議URL、Outlook URLを保持できる。
- [x] 非公開予定を設定に従いマスクまたは保存できる。
- [x] 日付・期間・キーワードで保存済み予定を検索できる。

## テスト計画

- イベント変換、UTC変換、終日、定期・例外、キャンセル、非公開マスクの単体テストを行う。
- 複数ページ、同一IDのUPSERT、参加者・場所・会議URLのSQLite結合テストを行う。
- `go test ./...` とCLIのhelp/status確認を実行する。
- 実環境では個人予定、Teams会議、終日、定期予定を少量取得して確認する。

## リスク

定期予定は発生・例外・series masterを別々に扱う必要がある。表示時のタイムゾーン変換と、非公開予定のMCP公開制御を混同しない。

## 変更履歴

`CHANGES.md` impact: yes

項目案:

- Outlook Calendarを初回同期してCLI検索できるようになった。

## 注記

Calendar deltaとウィンドウ管理は次のイシューで実装する。

- 2026-07-11: `internal/calendar`と`internal/outlookstore/calendar.go`、`calendar list/show/sync/day/range/search/status`を実装。`Prefer: outlook.timezone="UTC"`でUTC正規化し元タイムゾーンを保持、終日予定は元タイムゾーンの日付で表示。未取得のseries masterは1回だけ個別取得。非公開予定はraw_jsonごとマスク。実環境での少量取得確認は未実施。
- 2026-07-15: origin/mainで実装と自動テストを確認し、実装済みとして完了へ移行する。
- 2026-07-15: CLI名を `outlook` に統一した。
