# Teams Knowledge Sync 仕様書

## 1. 概要

Microsoft Teams 上の指定チームおよび自分が参加するチャットを継続的に取得し、ローカルDBへ同期する。

Claude Code、CodexなどのCoding AgentからTeamsを直接参照するのではなく、同期済みデータを検索するMCPサーバーを提供する。

本システムはTeamsの正式なアーカイブではなく、検索・参照を高速化するためのローカル検索インデックスとして扱う。

---

## 2. 目的

* Teams MCPからMicrosoft Graphを都度呼び出す際の遅延を削減する
* 複数のチーム、チャネル、チャットを横断検索できるようにする
* Coding Agentが必要な会話だけを少ないコンテキストで取得できるようにする
* Teams上のプロジェクト経緯、決定事項、依頼事項を検索可能にする
* 元のTeamsメッセージへ遷移できる状態を維持する

---

## 3. 対象範囲

### 3.1 取得対象

以下のTeamsデータを取得する。

#### 指定チーム

* チーム
* チャネル
* チャネル投稿
* 投稿への返信
* 投稿者
* メンション
* リアクション
* 添付ファイル情報
* 作成日時
* 更新日時
* 削除状態
* Teams上のメッセージURL

#### 自分が参加するチャット

* 1対1チャット
* グループチャット
* 会議チャット
* チャットメッセージ
* 投稿者
* メンション
* リアクション
* 添付ファイル情報
* 作成日時
* 更新日時
* 削除状態
* Teams上のメッセージURL

### 3.2 対象外

初期バージョンでは以下を対象外とする。

* Teamsへの投稿
* Teamsメッセージへの返信
* 添付ファイル本体の保存
* SharePoint、OneDrive上のファイル本文のインデックス
* Microsoft Purview相当の正式な監査・保持機能
* 全社Teamsデータの一括収集
* ベクトル検索
* AIによる自動要約の永続保存
* Web管理画面

---

## 4. 技術構成

| 項目        | 採用技術                         |
| --------- | ---------------------------- |
| 実装言語      | Go                           |
| データベース    | SQLite                       |
| 全文検索      | SQLite FTS5またはN-gram検索       |
| Teams API | Microsoft Graph API          |
| 認証        | Microsoft Entra ID OAuth 2.0 |
| 設定形式      | YAML                         |
| MCP       | Go製MCPサーバー                   |
| 配布形式      | 単一バイナリ                       |
| 動作形態      | CLIおよび常駐プロセス                 |

DuckDBは初期構成では採用しない。

将来、長期間の集計やParquet出力などの分析要件が発生した場合に、SQLiteを参照する分析用DBとして追加する。

---

## 5. システム構成

```text
Microsoft Teams
    ↓
Microsoft Graph API
    ↓
Teams Sync Service
    ├─ 認証
    ├─ チーム取得
    ├─ チャネル取得
    ├─ チャット取得
    ├─ メッセージ取得
    ├─ 差分同期
    └─ 再同期
         ↓
SQLite
    ├─ コンテナ情報
    ├─ メッセージ
    ├─ メンション
    ├─ リアクション
    ├─ 添付ファイル参照
    ├─ 同期状態
    └─ 検索インデックス
         ↓
Teams Knowledge MCP
         ↓
Claude Code / Codex
```

---

## 6. 基本方針

### 6.1 Graph APIとMCPを分離する

MCPサーバーからMicrosoft Graph APIを直接呼び出さない。

MCPサーバーは原則としてSQLiteのみ参照する。

最新情報が必要な場合は、同期サービスを実行してからMCP経由で検索する。

### 6.2 SQLiteを正規データストアとする

取得済みTeamsデータはSQLiteへ保存する。

同期処理は何度実行しても同じ結果になるよう、UPSERTを基本とする。

### 6.3 Teamsを正とする

SQLiteは検索用コピーであり、正式な情報源はTeamsとする。

各メッセージにはTeams上のURLを保存し、利用者が原文を確認できるようにする。

### 6.4 削除・編集に追随する

Teams上で編集されたメッセージはSQLiteにも反映する。

Teams上で削除されたメッセージは、本文を削除または無効化する。

初期実装では削除時に以下を行う。

* `deleted_at`を設定する
* `body_html`をNULLにする
* `body_text`をNULLにする
* 検索インデックスから除外する
* メッセージIDおよび削除日時は保持する

### 6.5 ポーリングから開始する

初期バージョンではMicrosoft Graph Change Notificationsを使用しない。

一定間隔でポーリングし、更新されたメッセージを取得する。

Change Notificationsは将来追加可能な構造とする。

---

## 7. 実行モード

### 7.1 CLIモード

手動で認証、確認、同期、検索を実行する。

```text
teams-knowledge auth login
teams-knowledge auth status

teams-knowledge team list
teams-knowledge channel list --team TEAM_ID
teams-knowledge chat list

teams-knowledge sync team --team TEAM_ID
teams-knowledge sync chats
teams-knowledge sync all

teams-knowledge search "検索語"
teams-knowledge thread MESSAGE_ID
teams-knowledge status
```

### 7.2 Daemonモード

定期的にTeamsデータを同期する。

```text
teams-knowledge daemon
```

Daemonは以下を行う。

* 設定済みチーム一覧の確認
* チャネル一覧の更新
* 自分が参加するチャット一覧の更新
* メッセージの差分同期
* 同期エラーの記録
* レート制限発生時の待機と再試行
* 一定期間ごとの再同期

### 7.3 MCPモード

MCPサーバーとして起動する。

```text
teams-knowledge mcp
```

初期バージョンではstdio transportを使用する。

必要になった場合はHTTP transportを追加する。

---

## 8. 認証

### 8.1 認証方式

ユーザー委任権限を使用する。

「サインインしたユーザーがTeams上で閲覧可能な範囲」のみを取得対象とする。

初期実装ではDevice Code Flowを使用する。

### 8.2 アプリケーション権限

初期バージョンでは使用しない。

以下のような全社参照権限を前提としない。

* `Chat.Read.All`
* `ChannelMessage.Read.All`

必要になった場合は、別の運用モードとして設計する。

### 8.3 トークン保存

アクセストークンおよび更新用トークンをSQLiteへ平文保存しない。

保存方式は以下の優先順位とする。

1. OSの資格情報ストア
2. 暗号化されたローカルファイル
3. 1Password CLI連携

WindowsではWindows Credential ManagerまたはDPAPIの利用を優先する。

---

## 9. 同期仕様

## 9.1 初回同期

初回同期では、設定された期間まで遡ってメッセージを取得する。

デフォルト値は365日とする。

設定により変更可能とする。

```yaml
sync:
  initial_lookback_days: 365
```

### 9.2 通常同期

通常同期では、コンテナ単位に保存された最終同期日時以降のデータを取得する。

ただし、Teams側の編集やAPIの遅延を考慮し、一定時間重複して取得する。

デフォルトでは最終同期日時の24時間前から再取得する。

```yaml
sync:
  overlap_duration: 24h
```

取得済みメッセージはUPSERTする。

### 9.3 完全再同期

指定したチーム、チャネル、チャットを再取得できるものとする。

```text
teams-knowledge sync team --team TEAM_ID --full
teams-knowledge sync chat --chat CHAT_ID --full
```

完全再同期でも既存DBを削除せず、取得結果をUPSERTする。

### 9.4 ページング

Microsoft Graph APIのページングに対応する。

`@odata.nextLink`が存在する限り、次ページを取得する。

### 9.5 レート制限

HTTP 429を受信した場合は`Retry-After`を尊重する。

再試行には指数バックオフを使用する。

再試行回数の上限を超えた場合は、同期状態へエラーを保存して次のコンテナへ進む。

### 9.6 エラー分離

単一のチャネルまたはチャットでエラーが発生しても、全体同期を停止しない。

コンテナ単位で以下を記録する。

* 最終同期試行日時
* 最終成功日時
* 最終エラー
* 連続失敗回数

### 9.7 編集メッセージ

同一メッセージIDの`lastModifiedDateTime`が更新されている場合は本文を更新する。

本文更新時に検索インデックスも更新する。

### 9.8 削除メッセージ

APIレスポンスから削除状態を判別できる場合は、DBへ削除状態を反映する。

通常同期だけでは削除を検出できない場合があるため、定期的に過去データを再取得する。

---

## 10. データモデル

## 10.1 containers

チームチャネルおよびチャットを共通のコンテナとして管理する。

```sql
CREATE TABLE containers (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    team_id TEXT,
    channel_id TEXT,
    chat_id TEXT,
    display_name TEXT,
    description TEXT,
    web_url TEXT,
    is_enabled INTEGER NOT NULL DEFAULT 1,
    last_message_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

`type`は以下のいずれかとする。

* `team_channel`
* `one_on_one_chat`
* `group_chat`
* `meeting_chat`

## 10.2 messages

```sql
CREATE TABLE messages (
    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL,
    container_id TEXT NOT NULL,
    parent_message_id TEXT,
    sender_id TEXT,
    sender_name TEXT,
    sender_type TEXT,
    body_html TEXT,
    body_text TEXT,
    message_type TEXT,
    subject TEXT,
    web_url TEXT,
    created_at TEXT,
    modified_at TEXT,
    deleted_at TEXT,
    etag TEXT,
    content_hash TEXT,
    raw_json TEXT NOT NULL,
    indexed_at TEXT,
    UNIQUE(container_id, id),
    FOREIGN KEY(container_id) REFERENCES containers(id)
);
```

## 10.3 mentions

```sql
CREATE TABLE message_mentions (
    message_row_id INTEGER NOT NULL,
    mention_id INTEGER,
    mentioned_user_id TEXT,
    mentioned_name TEXT,
    mentioned_type TEXT,
    FOREIGN KEY(message_row_id) REFERENCES messages(row_id)
);
```

## 10.4 reactions

```sql
CREATE TABLE message_reactions (
    message_row_id INTEGER NOT NULL,
    reaction_type TEXT NOT NULL,
    user_id TEXT,
    user_name TEXT,
    created_at TEXT,
    FOREIGN KEY(message_row_id) REFERENCES messages(row_id)
);
```

## 10.5 attachments

初期バージョンでは添付ファイル本体を保存しない。

```sql
CREATE TABLE attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_row_id INTEGER NOT NULL,
    attachment_id TEXT,
    attachment_type TEXT,
    name TEXT,
    content_url TEXT,
    content_type TEXT,
    drive_item_id TEXT,
    raw_json TEXT,
    FOREIGN KEY(message_row_id) REFERENCES messages(row_id)
);
```

## 10.6 sync_states

```sql
CREATE TABLE sync_states (
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    cursor TEXT,
    last_attempt_at TEXT,
    last_success_at TEXT,
    last_full_sync_at TEXT,
    last_error TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(resource_type, resource_id)
);
```

## 10.7 users

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    display_name TEXT,
    email TEXT,
    user_principal_name TEXT,
    raw_json TEXT,
    updated_at TEXT NOT NULL
);
```

---

## 11. 本文変換

Teamsメッセージ本文はHTML形式で保存される場合がある。

以下の2種類を保存する。

* `body_html`
* `body_text`

`body_html`にはGraph APIから取得した本文を原形で保存する。

`body_text`には検索用のプレーンテキストを保存する。

変換時は以下を行う。

* HTMLタグを除去する
* `<br>`、`<p>`などを改行へ変換する
* HTMLエンティティをデコードする
* メンション表示名を残す
* 不要な連続空白を整理する
* URLを残す
* コードブロックの改行を可能な範囲で維持する

---

## 12. 検索仕様

### 12.1 初期検索方式

初期バージョンでは以下のいずれかを採用する。

1. SQLite FTS5
2. アプリケーション側で生成したN-gram検索列

日本語検索精度を優先し、N-gram方式を基本候補とする。

### 12.2 検索対象

* メッセージ本文
* 件名
* 投稿者名
* チーム名
* チャネル名
* チャット名
* メンション先
* 添付ファイル名

### 12.3 検索条件

以下の条件を組み合わせられるものとする。

* キーワード
* チーム
* チャネル
* チャット
* 投稿者
* 参加者
* 開始日時
* 終了日時
* 自分へのメンション有無
* 返信のみ
* ルート投稿のみ
* 削除済みを含むか
* 最大件数

### 12.4 検索結果

検索結果には以下を含める。

* メッセージID
* コンテナID
* チーム名
* チャネル名またはチャット名
* 投稿者
* 投稿日時
* 本文抜粋
* 親メッセージID
* Teams URL
* 検索スコア

---

## 13. MCP仕様

## 13.1 基本方針

MCPツールはTeamsの構造を意識した高水準APIとして提供する。

汎用SQL実行ツールは提供しない。

書き込み系ツールは提供しない。

## 13.2 search_messages

Teamsメッセージを横断検索する。

入力例:

```json
{
  "query": "工事引継",
  "teams": ["ICT推進室"],
  "from": "2026-06-01T00:00:00+09:00",
  "to": "2026-07-01T00:00:00+09:00",
  "mentioned_me": false,
  "limit": 20
}
```

出力:

* 該当メッセージ一覧
* 本文抜粋
* 投稿者
* 投稿日時
* 所属チームまたはチャット
* Teams URL

## 13.3 get_thread

指定メッセージが属するスレッド全体を取得する。

入力:

* `message_id`
* `container_id`

出力:

* ルート投稿
* 返信一覧
* 投稿日時順の会話

チャットの場合は、指定メッセージ前後の会話を返す。

## 13.4 get_conversation_context

指定メッセージの前後を取得する。

入力:

* `message_id`
* `before`
* `after`

デフォルト値:

* `before`: 10
* `after`: 10

## 13.5 get_recent_updates

指定範囲の最近の投稿を取得する。

入力:

* チーム
* チャネル
* チャット
* 開始日時
* 最大件数

## 13.6 messages_mentioning_me

自分へのメンションを含む投稿を取得する。

入力:

* 開始日時
* 終了日時
* 未確認条件
* チームまたはチャット

初期バージョンでは「未確認」の正式な既読判定は行わない。

## 13.7 find_messages_by_participant

指定ユーザーが投稿または参加した会話を取得する。

入力:

* ユーザー名
* ユーザーID
* 期間
* キーワード

## 13.8 open_in_teams

指定メッセージのTeams URLを返す。

入力:

* `message_id`
* `container_id`

---

## 14. 設定ファイル

設定ファイルはYAML形式とする。

```yaml
database:
  path: ./data/teams-knowledge.db

entra:
  tenant_id: ${TEAMS_TENANT_ID}
  client_id: ${TEAMS_CLIENT_ID}

sync:
  interval: 5m
  initial_lookback_days: 365
  overlap_duration: 24h
  full_resync_interval: 168h
  request_timeout: 30s
  max_retries: 5
  concurrency: 4

teams:
  - id: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
    enabled: true
    channels:
      include_all: true
      exclude_ids: []

chats:
  include_my_chats: true
  include_one_on_one: true
  include_group: true
  include_meeting: true
  exclude_ids: []

search:
  engine: ngram
  ngram_size: 2

logging:
  level: info
  format: json
```

設定内ではチーム名やチャネル名ではなく、IDを正とする。

表示名は選択時の補助情報として扱う。

---

## 15. CLI仕様

### 15.1 認証

```text
teams-knowledge auth login
teams-knowledge auth logout
teams-knowledge auth status
```

### 15.2 対象確認

```text
teams-knowledge team list
teams-knowledge channel list --team TEAM_ID
teams-knowledge chat list
```

### 15.3 同期

```text
teams-knowledge sync all
teams-knowledge sync team --team TEAM_ID
teams-knowledge sync channel --channel CHANNEL_ID
teams-knowledge sync chats
teams-knowledge sync chat --chat CHAT_ID
```

オプション:

```text
--full
--since YYYY-MM-DD
--until YYYY-MM-DD
--dry-run
```

### 15.4 検索

```text
teams-knowledge search "工事引継"
teams-knowledge search "工事引継" --team TEAM_ID
teams-knowledge search "工事引継" --from 2026-06-01
teams-knowledge search "工事引継" --mentioned-me
```

### 15.5 状態確認

```text
teams-knowledge status
teams-knowledge status --json
```

表示項目:

* 最終同期日時
* 最終成功日時
* 対象チーム数
* 対象チャネル数
* 対象チャット数
* メッセージ数
* 同期エラー
* DBサイズ

---

## 16. ログ

ログは構造化JSON形式を標準とする。

以下の情報を記録する。

* 時刻
* ログレベル
* 処理種別
* リソース種別
* リソースID
* HTTPステータス
* 取得件数
* 処理時間
* 再試行回数
* エラー内容

アクセストークン、更新用トークン、本文全文はログへ出力しない。

---

## 17. ディレクトリ構成

```text
teams-knowledge/
├─ cmd/
│  └─ teams-knowledge/
│
├─ internal/
│  ├─ auth/
│  ├─ config/
│  ├─ graph/
│  ├─ sync/
│  ├─ store/
│  ├─ search/
│  ├─ mcp/
│  ├─ text/
│  └─ logging/
│
├─ migrations/
├─ testdata/
├─ data/
├─ go.mod
├─ go.sum
├─ README.md
└─ config.example.yaml
```

---

## 18. パッケージ責務

### internal/auth

* OAuth認証
* Device Code Flow
* トークン更新
* トークンキャッシュ
* 資格情報ストア連携

### internal/graph

* Microsoft Graph APIクライアント
* リクエスト生成
* ページング
* レート制限処理
* Graphレスポンス型
* ドメインモデルへの変換

Graph固有の型を他パッケージへ直接公開しない。

### internal/sync

* 同期対象の列挙
* 初回同期
* 差分同期
* 完全再同期
* エラー分離
* 同期状態更新

### internal/store

* SQLite接続
* マイグレーション
* UPSERT
* トランザクション
* 削除反映
* 同期状態保存

### internal/search

* 全文インデックス生成
* N-gram生成
* 検索条件組み立て
* 検索結果整形

### internal/mcp

* MCPサーバー
* MCPツール定義
* 入力検証
* SQLite検索呼び出し
* 出力整形

### internal/text

* HTMLからプレーンテキストへの変換
* 空白正規化
* N-gram生成
* 本文抜粋生成

---

## 19. ドメインモデル

Graph APIレスポンスをそのままDB層へ渡さない。

内部モデルへ変換する。

```go
type Message struct {
    ID              string
    ContainerID     string
    ParentMessageID string
    SenderID        string
    SenderName      string
    SenderType      string
    BodyHTML        string
    BodyText        string
    Subject         string
    MessageType     string
    WebURL          string
    CreatedAt       time.Time
    ModifiedAt      *time.Time
    DeletedAt       *time.Time
    ETag            string
    RawJSON         json.RawMessage
}
```

---

## 20. SQLite設定

接続時に以下を設定する。

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
```

同期サービスとMCPサーバーが同一DBを同時に利用できるよう、WALモードを使用する。

SQLiteへの書き込みは同期サービスに限定する。

MCPサーバーは原則読み取り専用で接続する。

---

## 21. 非機能要件

### 21.1 性能

初期目標:

* 100万メッセージまで単一SQLiteで動作する
* キーワード検索を2秒以内に返す
* MCPの単純検索を1秒以内に返す
* 同期中もMCP検索を継続できる

### 21.2 再実行性

同じ同期処理を複数回実行しても重複データを作成しない。

### 21.3 可搬性

* Windows
* Linux
* macOS

で動作可能な構造とする。

初期の主要対象はWindowsおよびLinuxとする。

### 21.4 バックアップ

SQLiteファイルをバックアップ対象とする。

WALモード利用中の単純コピーは避け、SQLite Backup APIまたは整合性のあるバックアップ手段を使用する。

### 21.5 セキュリティ

* DBファイルへのアクセス権を実行ユーザーに限定する
* トークンをDBへ保存しない
* ログへ本文全文を出力しない
* Graph APIレスポンスの機密情報を不用意に公開しない
* MCPを外部公開しない
* HTTP transportを追加する場合は認証を必須とする

---

## 22. テスト方針

### 22.1 単体テスト

* Graphレスポンス変換
* HTML本文変換
* ページング
* N-gram生成
* UPSERT
* 編集反映
* 削除反映
* 検索条件
* MCP入力検証

### 22.2 結合テスト

モックGraphサーバーを使用して以下を確認する。

* 複数ページ取得
* HTTP 429
* HTTP 401
* HTTP 403
* HTTP 500
* タイムアウト
* 編集メッセージ
* 削除メッセージ
* チャネル返信
* 会議チャット

### 22.3 実環境テスト

検証用チームおよび検証用チャットで以下を確認する。

* 初回同期
* 再同期
* 日本語検索
* メンション検索
* スレッド取得
* Teams URL遷移
* Claude CodeからのMCP利用
* CodexからのMCP利用

---

## 23. 実装フェーズ

### Phase 1: CLI同期

実装対象:

* 設定読込
* Device Code Flow
* チーム一覧
* チャネル一覧
* チャット一覧
* メッセージ取得
* SQLite保存
* CLI検索

完了条件:

* 指定チームを同期できる
* 自分のチャットを同期できる
* SQLiteからキーワード検索できる
* 同じ同期を再実行しても重複しない

### Phase 2: 常駐同期

実装対象:

* Daemonモード
* 定期実行
* 同期状態
* リトライ
* レート制限
* 完全再同期
* 構造化ログ

完了条件:

* 無人で継続同期できる
* コンテナ単位のエラーで全体停止しない
* 編集・削除を反映できる

### Phase 3: MCP

実装対象:

* MCP stdio transport
* `search_messages`
* `get_thread`
* `get_conversation_context`
* `get_recent_updates`
* `messages_mentioning_me`
* `open_in_teams`

完了条件:

* Claude Codeから検索できる
* Codexから検索できる
* MCPがGraph APIを直接呼び出さない
* 検索結果からTeams原文へ遷移できる

### Phase 4: 検索改善

実装候補:

* 日本語N-gram検索改善
* 検索ランキング調整
* 添付ファイル名検索
* 参加者検索
* 会話単位の取得
* 日付表現の正規化
* 定型フィルター

### Phase 5: 拡張

実装候補:

* Microsoft Graph Change Notifications
* SharePoint、OneDriveファイル本文取得
* DuckDB分析
* Parquetエクスポート
* HTTP MCP transport
* 複数ユーザー対応
* 暗号化DB
* AI要約キャッシュ

---

## 24. 完了条件

初期リリースは以下を満たした時点とする。

* Goの単一バイナリとして配布できる
* Device Code Flowで認証できる
* 指定チームを設定できる
* 自分が参加するチャットを取得できる
* チャネル投稿と返信を取得できる
* メッセージをSQLiteへ保存できる
* 再実行時に重複しない
* 編集メッセージを更新できる
* 削除メッセージを検索対象から除外できる
* 日本語キーワード検索ができる
* MCPからメッセージを検索できる
* MCPからスレッドを取得できる
* Teams原文URLを返せる
* 同期エラーを確認できる
* 認証トークンを平文保存しない

---

## 25. 設計上の判断

### Goを採用する理由

* HTTP、JSON、SQLite、常駐処理との相性がよい
* 単一バイナリで配布しやすい
* 試行錯誤時の変更コストが低い
* Claude Code、Codexによる修正が比較的容易
* Rustより非同期処理と型設計の初期負担が小さい

### SQLiteを採用する理由

* メッセージ単位のUPSERTに適する
* 編集・削除の反映に適する
* トランザクションを扱いやすい
* ローカル検索の遅延が小さい
* FTS5を利用できる
* 単一ファイルで運用できる
* MCPと同期サービスから同時参照できる

### DuckDBを初期採用しない理由

* 継続的な小単位UPSERTはSQLiteの方が適する
* 初期要件は分析より検索と同期が中心
* DuckDBは将来の集計、Parquet出力、分析用途として追加できる

---

## 26. 将来のDuckDB連携

SQLiteを稼働系DB、DuckDBを分析系DBとして分離する。

```text
SQLite
    ↓
DuckDB
    ├─ 長期間集計
    ├─ ユーザー別分析
    ├─ プロジェクト別分析
    ├─ Parquet出力
    └─ BI連携
```

SQLiteをDuckDBへ置き換えるのではなく、必要な場合だけ分析レイヤーとして追加する。

