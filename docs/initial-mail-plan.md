# Outlook Mail Knowledge Sync 仕様書

## 1. 概要

Microsoft Outlookのメールボックスから、登録済みメールアドレスに関係するメールを継続的に取得し、ローカルSQLiteへ同期する。

Claude Code、CodexなどのCoding AgentからMicrosoft Graphを直接参照するのではなく、同期済みメールを検索するMCPサーバーを提供する。

本システムは正式なメールアーカイブではなく、個人または部署業務における検索・参照を高速化するためのローカル検索インデックスとして扱う。

---

## 2. 目的

* Outlook MCPからMicrosoft Graphを都度呼び出す際の遅延を削減する
* 複数のメールアドレス宛てに届いたメールを横断検索する
* メーリングリスト、部署アドレス、個人アドレスを同一基盤で検索する
* Coding Agentが必要なメール本文とスレッドだけを取得できるようにする
* 添付ファイルや元メールへの参照を維持する
* プロジェクト経緯、依頼、回答、決定事項をメールから検索可能にする

---

## 3. 取得対象

### 3.1 対象メールボックス

初期バージョンでは、Microsoft Entra IDでサインインしたユーザー自身のExchange Onlineメールボックスを対象とする。

他ユーザーのメールボックス、共有メールボックス、Microsoft 365グループメールボックスは初期対象外とする。

将来、明示的な権限を持つメールボックスを追加できる構造とする。

### 3.2 登録メールアドレス

収集対象となるメールアドレスを設定ファイルへ登録する。

例:

```yaml
mail:
  addresses:
    - address: hirohito.fujita@example.com
      name: 個人
      enabled: true

    - address: ict@example.com
      name: ICT推進室
      enabled: true

    - address: system-admin@example.com
      name: システム管理ML
      enabled: true
```

登録対象には以下を含められる。

* 個人メールアドレス
* メールエイリアス
* メーリングリスト
* 配布グループ
* 部署代表アドレス
* プロジェクト用メールアドレス

### 3.3 対象判定

受信メールについて、以下のいずれかに登録メールアドレスが含まれる場合に保存対象とする。

* `toRecipients`
* `ccRecipients`
* `bccRecipients`
* `internetMessageHeaders`内の対象ヘッダー

対象ヘッダーの初期値:

* `To`
* `Cc`
* `Delivered-To`
* `X-Original-To`
* `Envelope-To`
* `X-Envelope-To`

メールアドレスは以下の処理後に比較する。

* 前後空白の除去
* 小文字化
* 表示名の除去
* `mailto:`の除去
* 山括弧内アドレスの抽出

比較は完全一致とする。

### 3.4 配布グループとメーリングリスト

配布グループ経由のメールでは、Graph APIの受信者情報に最終受信者しか含まれない場合や、元のメーリングリストアドレスがヘッダーに残らない場合がある。

そのため、対象判定は以下の優先順位とする。

1. Graphの受信者プロパティ
2. RFCメールヘッダー
3. 件名・本文などを利用した明示的な補助ルール
4. 対象フォルダーによる判定

補助ルールは設定可能とする。

```yaml
mail:
  addresses:
    - address: construction-dx@example.com
      name: 工事DX ML
      match:
        headers:
          - to
          - cc
          - delivered-to
          - x-original-to
        subject_prefixes:
          - "[construction-dx]"
```

### 3.5 送信メール

送信済みメールは設定により取得可能とする。

送信メールでは以下のいずれかに登録メールアドレスが含まれるものを対象とする。

* `from`
* `sender`
* `toRecipients`
* `ccRecipients`
* `bccRecipients`

初期値では送信済みメールも取得する。

```yaml
mail:
  include_received: true
  include_sent: true
```

### 3.6 メールフォルダー

初期対象:

* Inbox
* Sent Items
* Archive
* ユーザーが指定したフォルダー

初期対象外:

* Deleted Items
* Junk Email
* Drafts
* Outbox
* Sync Issues

設定例:

```yaml
mail:
  folders:
    include:
      - inbox
      - sentitems
      - archive
    exclude:
      - deleteditems
      - junkemail
      - drafts
```

---

## 4. 対象データ

メールごとに以下を取得する。

* GraphメッセージID
* Internet Message ID
* Conversation ID
* Conversation Index
* 件名
* 本文
* 本文形式
* 本文プレビュー
* 差出人
* Sender
* To
* Cc
* Bcc
* Reply-To
* 受信日時
* 送信日時
* 作成日時
* 更新日時
* 既読状態
* 重要度
* フラグ
* カテゴリ
* 添付ファイル有無
* 添付ファイルのメタデータ
* メールフォルダー
* Web URL
* RFCメールヘッダー
* 削除状態
* 元のGraph JSON

Microsoft Graphのmessageリソースは、To、Cc、Bccなどの受信者情報と、差分同期・変更通知をサポートする。

---

## 5. 対象外

初期バージョンでは以下を対象外とする。

* メール送信
* 返信
* 転送
* 下書き作成
* メールの移動
* 既読状態の変更
* カテゴリの変更
* 添付ファイル本文の全文検索
* S/MIME暗号化メールの復号
* Microsoft Purview相当の保持
* メールボックス全体の正式なバックアップ
* Web管理画面
* ベクトル検索

---

## 6. 技術構成

| 項目     | 採用技術                         |
| ------ | ---------------------------- |
| 実装言語   | Go                           |
| データベース | SQLite                       |
| 全文検索   | SQLite FTS5またはN-gram         |
| メールAPI | Microsoft Graph API          |
| 認証     | Microsoft Entra ID OAuth 2.0 |
| 設定形式   | YAML                         |
| MCP    | Go製MCPサーバー                   |
| 配布形式   | 単一バイナリ                       |
| 動作形態   | CLI、Daemon、MCP               |

---

## 7. システム構成

```text
Exchange Online
    ↓
Microsoft Graph API
    ↓
Outlook Mail Sync
    ├─ メールフォルダー取得
    ├─ メッセージ差分取得
    ├─ 登録アドレス判定
    ├─ 本文変換
    ├─ 添付情報取得
    └─ 削除・移動反映
         ↓
SQLite
    ├─ メール
    ├─ 送受信者
    ├─ 登録アドレス
    ├─ 添付ファイル
    ├─ メールヘッダー
    ├─ 同期状態
    └─ 検索インデックス
         ↓
Outlook Mail MCP
         ↓
Claude Code / Codex
```

---

## 8. 基本方針

### 8.1 Graph APIとMCPを分離する

MCPサーバーはMicrosoft Graph APIを直接呼び出さない。

MCPサーバーはSQLiteを読み取り専用で参照する。

### 8.2 メールボックス全体を同期してから対象判定する

Microsoft Graph側の検索条件だけに対象アドレス判定を依存しない。

対象フォルダーの差分を取得し、アプリケーション側で登録アドレスとの一致を判定する。

これにより以下へ対応する。

* 配布グループ
* エイリアス
* メーリングリスト
* メールヘッダーによる判定
* 将来の判定ルール変更

### 8.3 SQLiteには対象メールだけを保存する

初期バージョンでは、登録アドレスに一致しないメール本文を永続保存しない。

ただし、同期処理上必要な最小限のID、更新日時、判定結果は保存可能とする。

### 8.4 Outlookを正とする

SQLiteは検索用コピーとする。

検索結果にはOutlook WebのURLを含め、原文を確認できるようにする。

### 8.5 削除・移動を追跡する

メール差分取得はフォルダー単位で管理する。

メールが別フォルダーへ移動された場合、旧フォルダーで削除、新フォルダーで追加として観測される可能性を考慮する。

Internet Message IDなどを用いて同一メールを関連付ける。

---

## 9. 実行モード

### 9.1 CLI

```text
outlook-knowledge mail auth login
outlook-knowledge mail auth status

outlook-knowledge mail address list
outlook-knowledge mail folder list

outlook-knowledge mail sync
outlook-knowledge mail sync --folder FOLDER_ID
outlook-knowledge mail sync --full

outlook-knowledge mail search "工事引継"
outlook-knowledge mail thread MESSAGE_ID
outlook-knowledge mail show MESSAGE_ID
outlook-knowledge mail status
```

### 9.2 Daemon

```text
outlook-knowledge mail daemon
```

実行内容:

* メールフォルダー差分同期
* 登録アドレス判定
* メールのUPSERT
* 移動・削除反映
* 添付ファイル情報取得
* 検索インデックス更新
* エラー記録
* 定期完全照合

### 9.3 MCP

```text
outlook-knowledge mail mcp
```

初期バージョンではstdio transportを使用する。

---

## 10. 認証

### 10.1 認証方式

ユーザー委任権限を使用する。

初期実装ではDevice Code Flowを使用する。

### 10.2 想定権限

初期候補:

* `User.Read`
* `Mail.Read`
* `offline_access`

メールへの変更権限は要求しない。

### 10.3 トークン保存

以下の優先順位とする。

1. OS資格情報ストア
2. DPAPI等で暗号化したファイル
3. 1Password CLI

トークンをSQLiteへ平文保存しない。

---

## 11. 同期方式

### 11.1 メールフォルダー同期

最初にメールフォルダー階層を取得する。

メールフォルダー自体についても差分情報を保持する。

### 11.2 メッセージ差分同期

Microsoft Graphのメッセージ差分取得はフォルダー単位で行う。

各フォルダーに対して個別のdelta linkを保存する。

```text
mail folder
    ├─ initial delta
    ├─ @odata.nextLink
    └─ @odata.deltaLink
```

`@odata.nextLink`がある間はページングを継続し、完了時の`@odata.deltaLink`を保存する。

### 11.3 初回同期

初回同期は以下のいずれかで制限できる。

* 期間
* 最大メール件数
* フォルダー
* 登録アドレス

デフォルト:

```yaml
sync:
  mail_initial_lookback_days: 365
```

Graphのdelta初回取得で期間制限が適用しにくい場合は、初回一覧取得と差分同期の初期化を分けて実装する。

### 11.4 対象判定の再実行

登録メールアドレスや判定ルールが変更された場合、保存済みメタデータに対して対象判定を再実行できるものとする。

```text
outlook-knowledge mail reclassify
```

### 11.5 削除

削除を検出した場合:

* `deleted_at`を設定する
* 本文を削除する
* 添付ファイルメタデータを削除する
* 検索インデックスから除外する
* IDと削除日時を保持する

### 11.6 レート制限

HTTP 429では`Retry-After`を尊重する。

指数バックオフを使用し、フォルダー単位でエラーを分離する。

---

## 12. データモデル

### 12.1 registered_addresses

```sql
CREATE TABLE registered_addresses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT NOT NULL UNIQUE,
    display_name TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    match_to INTEGER NOT NULL DEFAULT 1,
    match_cc INTEGER NOT NULL DEFAULT 1,
    match_bcc INTEGER NOT NULL DEFAULT 1,
    match_headers INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

### 12.2 mail_folders

```sql
CREATE TABLE mail_folders (
    id TEXT PRIMARY KEY,
    parent_folder_id TEXT,
    display_name TEXT NOT NULL,
    well_known_name TEXT,
    child_folder_count INTEGER,
    total_item_count INTEGER,
    unread_item_count INTEGER,
    is_hidden INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

### 12.3 messages

```sql
CREATE TABLE mail_messages (
    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    immutable_id TEXT,
    internet_message_id TEXT,
    conversation_id TEXT,
    conversation_index TEXT,
    folder_id TEXT NOT NULL,

    subject TEXT,
    body_html TEXT,
    body_text TEXT,
    body_preview TEXT,
    body_content_type TEXT,

    sender_address TEXT,
    sender_name TEXT,
    from_address TEXT,
    from_name TEXT,

    received_at TEXT,
    sent_at TEXT,
    created_at TEXT,
    modified_at TEXT,
    deleted_at TEXT,

    importance TEXT,
    is_read INTEGER,
    is_draft INTEGER,
    has_attachments INTEGER,
    flag_status TEXT,

    web_url TEXT,
    etag TEXT,
    content_hash TEXT,
    raw_json TEXT NOT NULL,
    indexed_at TEXT,

    FOREIGN KEY(folder_id) REFERENCES mail_folders(id)
);
```

### 12.4 recipients

```sql
CREATE TABLE mail_recipients (
    message_row_id INTEGER NOT NULL,
    recipient_type TEXT NOT NULL,
    address TEXT NOT NULL,
    display_name TEXT,
    normalized_address TEXT NOT NULL,
    FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id)
);
```

`recipient_type`:

* `to`
* `cc`
* `bcc`
* `reply_to`

### 12.5 message_addresses

メールと登録アドレスの一致結果を保存する。

```sql
CREATE TABLE mail_message_addresses (
    message_row_id INTEGER NOT NULL,
    registered_address_id INTEGER NOT NULL,
    matched_by TEXT NOT NULL,
    matched_value TEXT,
    PRIMARY KEY(message_row_id, registered_address_id, matched_by),
    FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id),
    FOREIGN KEY(registered_address_id) REFERENCES registered_addresses(id)
);
```

`matched_by`:

* `to`
* `cc`
* `bcc`
* `from`
* `sender`
* `header`
* `subject_rule`
* `folder_rule`

### 12.6 headers

```sql
CREATE TABLE mail_headers (
    message_row_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id)
);
```

### 12.7 attachments

```sql
CREATE TABLE mail_attachments (
    id TEXT NOT NULL,
    message_row_id INTEGER NOT NULL,
    name TEXT,
    content_type TEXT,
    size INTEGER,
    is_inline INTEGER NOT NULL DEFAULT 0,
    content_id TEXT,
    attachment_type TEXT,
    raw_json TEXT,
    PRIMARY KEY(message_row_id, id),
    FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id)
);
```

添付ファイル本体は初期バージョンでは保存しない。

### 12.8 categories

```sql
CREATE TABLE mail_categories (
    message_row_id INTEGER NOT NULL,
    category TEXT NOT NULL,
    PRIMARY KEY(message_row_id, category),
    FOREIGN KEY(message_row_id) REFERENCES mail_messages(row_id)
);
```

### 12.9 sync_states

```sql
CREATE TABLE mail_sync_states (
    folder_id TEXT PRIMARY KEY,
    next_link TEXT,
    delta_link TEXT,
    last_attempt_at TEXT,
    last_success_at TEXT,
    last_full_sync_at TEXT,
    last_error TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0
);
```

---

## 13. 本文変換

以下を保存する。

* `body_html`
* `body_text`
* `body_preview`

HTMLからテキストへの変換時には以下を行う。

* HTMLタグ除去
* 段落と改行の維持
* 引用部分の識別
* 署名部分の識別
* HTMLエンティティのデコード
* URLの保持
* 不要なトラッキング画像の除外
* 連続空白の整理

初期バージョンでは引用・署名を削除せず、区間を識別可能な状態で保持する。

---

## 14. スレッド構築

基本的な会話グループ化には`conversationId`を使用する。

ただし、メーリングリストや外部メールシステムでは会話IDが分断される可能性があるため、以下も保存する。

* Internet Message ID
* In-Reply-To
* References
* Subject正規化結果

スレッド構築の優先順位:

1. `conversationId`
2. `In-Reply-To`
3. `References`
4. 正規化件名
5. 参加者と期間

件名正規化では以下の接頭辞を除去する。

* `Re:`
* `RE:`
* `Fw:`
* `FW:`
* `Fwd:`
* 日本語環境で付与される転送・返信接頭辞

---

## 15. 検索仕様

### 15.1 検索対象

* 件名
* 本文
* 差出人
* 宛先
* 登録アドレス名
* 添付ファイル名
* カテゴリ
* RFCヘッダー
* Conversation ID
* Internet Message ID

### 15.2 検索条件

* キーワード
* 登録アドレス
* 差出人
* 宛先
* Cc
* 期間
* フォルダー
* 添付ファイル有無
* 未読のみ
* 重要度
* カテゴリ
* 受信メールのみ
* 送信メールのみ
* 最大件数

### 15.3 検索結果

* メールID
* 件名
* 差出人
* 主な宛先
* 登録アドレス
* 受信日時または送信日時
* 本文抜粋
* 添付ファイル名
* Outlook URL
* 検索スコア

---

## 16. MCP仕様

MCPはメール構造を意識した高水準APIを提供する。

汎用SQLツールは提供しない。

### 16.1 search_mail

```json
{
  "query": "工事引継",
  "addresses": ["ict@example.com"],
  "from_date": "2026-06-01T00:00:00+09:00",
  "to_date": "2026-07-01T00:00:00+09:00",
  "sender": "example.co.jp",
  "has_attachments": true,
  "limit": 20
}
```

### 16.2 get_mail

指定メールの詳細を取得する。

入力:

* `message_id`

出力:

* 件名
* 本文
* 差出人
* 宛先
* 日時
* 添付情報
* Outlook URL

### 16.3 get_mail_thread

指定メールが属するスレッドを時系列で取得する。

入力:

* `message_id`
* `include_quoted_body`

### 16.4 get_mail_context

指定メール前後の関連メールを取得する。

### 16.5 get_recent_mail

登録アドレス別の最近のメールを取得する。

### 16.6 find_mail_by_participant

指定した人物またはドメインが関係するメールを取得する。

### 16.7 find_unanswered_mail

自分または登録アドレス宛てのメールについて、後続の送信メールが確認できないものを候補として返す。

これは推定結果であり、正式な未回答判定とはしない。

### 16.8 find_action_items

依頼、期限、確認事項を含む可能性のあるメールを検索する。

初期実装ではキーワード検索を行い、LLMによる判断はMCP利用側で行う。

### 16.9 open_in_outlook

指定メールのOutlook URLを返す。

---

## 17. 設定ファイル

```yaml
database:
  path: ./data/outlook-knowledge.db

entra:
  tenant_id: ${OUTLOOK_TENANT_ID}
  client_id: ${OUTLOOK_CLIENT_ID}

mail:
  include_received: true
  include_sent: true

  addresses:
    - address: hirohito.fujita@example.com
      name: 個人
      enabled: true

    - address: ict@example.com
      name: ICT推進室
      enabled: true

    - address: system-admin@example.com
      name: システム管理ML
      enabled: true
      match:
        subject_prefixes:
          - "[system-admin]"

  folders:
    include:
      - inbox
      - sentitems
      - archive
    exclude:
      - deleteditems
      - junkemail
      - drafts

sync:
  interval: 5m
  mail_initial_lookback_days: 365
  full_resync_interval: 168h
  request_timeout: 30s
  max_retries: 5
  concurrency: 2

search:
  engine: ngram
  ngram_size: 2

logging:
  level: info
  format: json
```

---

## 18. CLI仕様

```text
outlook-knowledge mail address list
outlook-knowledge mail address test MESSAGE_ID

outlook-knowledge mail folder list
outlook-knowledge mail sync
outlook-knowledge mail sync --full
outlook-knowledge mail sync --folder FOLDER_ID
outlook-knowledge mail reclassify

outlook-knowledge mail search "工事引継"
outlook-knowledge mail show MESSAGE_ID
outlook-knowledge mail thread MESSAGE_ID
outlook-knowledge mail status
```

---

## 19. 非機能要件

### 性能

初期目標:

* 100万メールまで単一SQLiteで動作する
* 通常検索を2秒以内に返す
* MCP検索を1秒から2秒以内に返す
* 同期中も検索を継続できる

### セキュリティ

* DBファイルのアクセス権を実行ユーザーに限定する
* MCPを外部公開しない
* トークンを平文保存しない
* メール本文をログへ出力しない
* 添付ファイル本体を初期状態では保存しない
* 削除されたメール本文をローカルにも残さない
* HTML本文の表示時にスクリプト等を実行しない

### 再実行性

同じdeltaページを複数回処理しても重複しない。

### バックアップ

SQLite Backup APIを利用する。

---

## 20. ディレクトリ構成

```text
outlook-knowledge/
├─ cmd/
│  └─ outlook-knowledge/
├─ internal/
│  ├─ auth/
│  ├─ config/
│  ├─ graph/
│  ├─ mail/
│  │  ├─ sync/
│  │  ├─ classifier/
│  │  ├─ thread/
│  │  └─ search/
│  ├─ calendar/
│  ├─ store/
│  ├─ mcp/
│  ├─ text/
│  └─ logging/
├─ migrations/
├─ testdata/
├─ go.mod
└─ config.example.yaml
```

---

## 21. 実装フェーズ

### Phase 1: Mail CLI

* Device Code Flow
* フォルダー一覧
* メール一覧取得
* 登録アドレス判定
* SQLite保存
* CLI検索
* Outlook URL取得

### Phase 2: 差分同期

* フォルダー単位delta
* delta link保存
* 移動・削除反映
* 再試行
* Daemon

### Phase 3: Mail MCP

* `search_mail`
* `get_mail`
* `get_mail_thread`
* `get_recent_mail`
* `find_mail_by_participant`
* `open_in_outlook`

### Phase 4: 検索改善

* 日本語N-gram
* 引用・署名識別
* スレッド復元
* メーリングリスト判定改善
* 未回答候補
* 添付ファイル本文取得

---

## 22. 完了条件

* 登録メールアドレスを複数設定できる
* 個人アドレスとメーリングリストを区別できる
* To、Cc、Bcc、メールヘッダーを使って対象判定できる
* 対象フォルダーを設定できる
* 受信メールと送信メールを同期できる
* メールをSQLiteへ保存できる
* フォルダー単位の差分同期ができる
* 編集、移動、削除を反映できる
* 日本語検索ができる
* スレッドを取得できる
* MCPから検索できる
* Outlook原文URLを返せる
* トークンを平文保存しない
* メール本文をログへ出力しない

