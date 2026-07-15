# Outlook MailにGraph経由のオンデマンド取得を追加する

Status: open
Model: GPT-5
Created: 2026-07-15
Updated: 2026-07-15
Branch: feat/20260715-outlook-mail-fetch

## 概要

`outlook` にTeamsの `message fetch` 相当のMail取得機能を追加し、ローカル未同期の指定メールと、そのメールが属するスレッドをMicrosoft Graphから取得して保存・表示できるようにする。

## 背景

現在の `outlook mail show` と `outlook mail thread` はSQLiteに保存済みのメールだけを参照する。Mail同期は登録アドレスに一致するメールを対象とするため、Graph上には存在するが未同期のスレッドメンバーを取得できない。

Teamsの `message fetch` は指定URLをGraphへ問い合わせてメッセージを保存し、チャネルの親メッセージでは返信も取得する。Outlook Mailにも同様のオンデマンド取得経路が必要である。

参考:

- [Get message - Microsoft Graph](https://learn.microsoft.com/en-us/graph/api/message-get?view=graph-rest-1.0)
- [List messages - Microsoft Graph](https://learn.microsoft.com/en-us/graph/api/user-list-messages?view=graph-rest-1.0)

## 問題

- 未同期のメールは、GraphのメッセージIDを知っていても `outlook mail show` で取得できない。
- `outlook mail thread MESSAGE_ID` はローカル保存済みの `conversation_id` 一致行だけを返すため、スレッド全体が欠落する可能性がある。
- Graphから取得したメールを既存の本文変換、添付メタデータ取得、UPSERT処理へ接続するCLIがない。

## 目標

- 指定したGraphメッセージIDをGraphから取得し、SQLiteへ保存して詳細表示できるようにする。
- 指定メールの `conversationId` を基にGraphから取得可能なスレッドメンバーをページング取得し、時系列で保存・表示できるようにする。
- 同じメッセージやスレッドを複数回取得しても重複せず、既存のdelta同期と共存できるようにする。

## 対象外

- バックグラウンドMail同期の対象範囲、登録アドレス分類、delta状態管理の変更
- メール送信、返信、転送、既読変更などの書き込み操作
- 添付ファイル本体のダウンロード
- Mail MCPの追加
- 共有メールボックスや他ユーザーのメールボックス対応

## 提案する方針

- `cmd/outlook/mail.go` に `outlook mail fetch MESSAGE_ID` を追加する。単一メッセージをGraphから取得し、既存の `mail.Message` 変換と `outlookstore.UpsertMailMessage`、添付メタデータ取得処理を再利用する。
- スレッド取得の指定方法（`fetch --thread` または既存 `thread` の取得オプション）は、既存CLIの互換性を保つ形で決定する。指定メッセージから `conversationId` を得て、Graphの対応するメッセージ一覧をページングし、取得結果を `received_at` 順に表示する。
- 明示的なオンデマンド取得はバックグラウンド同期の選別範囲を広げない。登録アドレスに一致しない取得結果を保存する場合は、通常検索への露出範囲と分類情報の扱いを実装前に定義し、テストで固定する。
- Graph取得失敗時は既存SQLiteの内容を破壊せず、HTTPエラーと対象IDをCLIエラーとして返す。
- `README.md` と `--help` に単一メッセージ取得およびスレッド取得の使用例、Graph権限、ローカル保存の動作を記載する。

## 受け入れ条件

- [ ] 未同期の有効なメッセージIDを指定すると、Graphから取得した件名、本文、送受信者、受信日時、Outlook URL、添付メタデータを保存して表示できる。
- [ ] スレッド取得を指定すると、Graphから取得可能な同一会話の全ページを取得し、ローカルに存在しないメンバーを含めて時系列で表示できる。
- [ ] 単一メッセージ取得とスレッド取得を再実行しても、Graph IDをキーに重複行や重複添付が作成されない。
- [ ] 取得対象の登録アドレス不一致時の保存・通常検索への表示方針がドキュメント化され、単体・結合テストで検証される。
- [ ] Graphの404、権限エラー、ページング途中の失敗時に既存データとdeltaリンクが保持される。
- [ ] `outlook mail fetch` とスレッド取得のヘルプ、README例が実装されたCLIの引数順と一致する。
- [ ] 既存のMail同期、検索、`show`、`thread`、Calendar、Teamsのテストが通過する。

## テスト計画

- Graphモックで単一メッセージ取得、conversationId取得、複数ページ取得、添付メタデータ取得、404・403・ページ途中の失敗を検証する。
- SQLite結合テストで未同期メッセージの保存、同一IDのUPSERT、スレッド順序、既存delta状態の不変性を検証する。
- `go test -race ./...`、`go vet ./...`、`go build ./cmd/outlook` を実行する。
- 実環境では同期対象外の既知メッセージIDを1件、返信を含むスレッドを1件取得し、Graph権限と表示内容を手動確認する。

## リスク

Graphのメッセージ一覧で `conversationId` による取得可否・制約があるため、APIが返す範囲を「全件」と誤認しない。登録アドレスに一致しないメールを明示取得で保存すると、通常検索の情報範囲が広がる可能性がある。大きなスレッドではページング量と本文・添付メタデータの保存量が増えるため、上限と中断時の再実行動作を定義する。

## 変更履歴

`CHANGES.md` impact: yes

項目案：

- Outlook MailがGraph経由の単一メッセージおよびスレッドのオンデマンド取得に対応した。

## 注記

実装前に、GraphのconversationIdフィルターと、明示取得した未登録アドレスのメールを通常検索へ含めるかを確定する。
