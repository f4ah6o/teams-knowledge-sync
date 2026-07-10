# Outlook Mail CLI初回同期を追加する

Status: done
Model: GPT-5
Created: 2026-07-10
Updated: 2026-07-10
Branch: feat/20260710-mail-cli-sync

## 概要

同一リポジトリに `outlook-knowledge` のMail機能を追加し、登録アドレスに関係するメールを初回取得してSQLite検索できるようにする。

## 背景

`docs/initial-mail-plan.md` は、本人のExchange Onlineメールボックス、複数の登録アドレス、受信者・ヘッダーによる完全一致判定、Inbox/Sent Items/Archive等のフォルダーを対象としている。既存Teamsの認証・Graph・SQLite・本文正規化は共通部品として再利用できる。

## 問題

現在のリポジトリにはMail用の設定、スキーマ、CLI、分類処理がなく、Outlookメールをローカル検索できない。

## 目標

登録メールアドレスに一致する受信・送信メールを初回取得し、本文・メタデータ・受信者・ヘッダー・添付メタデータをSQLiteへ保存してCLIから検索・表示できるようにする。

## 対象外

- Mail delta、Daemon、MCP
- 共有メールボックス・他ユーザーのメールボックス
- 添付ファイル本体の保存
- Deleted Items、Junk、Drafts、Outboxの初期対象化

## 提案する方針

以下の方針どおり実装した。

- `cmd/outlook-knowledge` を追加し、既存のDevice Code FlowとGraphクライアントを共有する。
- `mail.addresses`、`mail.folders`、`mail.include_received`、`mail.include_sent` と `mail_initial_lookback_days` を設定化する。
- フォルダー一覧を取得し、対象フォルダーごとに期間制限付きメッセージ一覧をページングする。
- アドレスは空白・表示名・`mailto:`・山括弧を正規化し、受信者、送信者、指定ヘッダー、設定済み件名ルールを優先順位どおり判定する。
- `mail_messages`、`mail_folders`、`mail_recipients`、`mail_message_addresses`、`mail_headers`、`mail_attachments`、FTSを追加し、Graph IDを一意キーとしてUPSERTする。
- `mail address/folder/sync/search/show/thread/status` を実装する。

## 受け入れ条件

- [x] 複数の登録メールアドレスを設定でき、正規化後の完全一致で分類できる。
- [x] 受信者・送信者・指定ヘッダー・件名ルールの一致理由を保存できる。
- [x] 初回取得の期間、対象フォルダー、受信・送信対象を設定で制限できる。
- [x] 複数ページの取得結果が重複せずSQLiteへ保存される。
- [x] 本文、送受信時刻、スレッド識別子、Outlook URL、添付メタデータを保持できる。
- [x] 削除対象フォルダーを初期取得しない。

## テスト計画

- アドレス正規化と分類理由の単体テストを行う。
- 複数ページ、HTML本文、ヘッダー、送信メール、対象外フォルダー、重複UPSERTの結合テストを行う。
- `go test ./...` とCLIのhelp/status確認を実行する。
- 実環境では本人メールボックスの少量フォルダーで認証、取得、検索、Outlook URLを確認する。

## リスク

Graphの受信者情報にメーリングリスト原アドレスが残らない場合があるため、判定理由を保存して再分類可能にする。メール本文はログへ出力せず、HTMLを実行しない。

## 変更履歴

`CHANGES.md` impact: yes

項目案:

- Outlook Mailを初回同期してCLI検索できるようになった。

## 注記

deltaリンクと定期Daemonは次のイシューで実装する。

- 2026-07-10: 依存順にoutlook-knowledge共有基盤を新設するMail CLI初回同期の実装へ着手。
- 2026-07-10: `go test -race ./...`、`go vet ./...`、CLIのhelpと`mail status --json`が成功した。Microsoft 365実環境での認証・取得確認は資格情報が必要なため未実施。
- 2026-07-10: Mail CLI初回同期の実装と自動テスト、CLI検証が完了したため完了。
