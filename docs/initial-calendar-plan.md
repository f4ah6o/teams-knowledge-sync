# Outlook Calendar Knowledge Sync 仕様書

## 1. 概要

Microsoft Outlookの予定表から予定、会議、定期予定を継続的に取得し、ローカルSQLiteへ同期する。

Claude Code、CodexなどのCoding AgentからMicrosoft Graphを直接参照するのではなく、同期済み予定を検索するMCPサーバーを提供する。

本システムは予定表の正式な記録基盤ではなく、検索・参照・業務コンテキスト取得を高速化するためのローカル検索インデックスとして扱う。

---

## 2. 目的

* Outlook Calendar MCPからMicrosoft Graphを都度呼び出す遅延を削減する
* 過去と今後の予定を横断検索する
* 会議名、参加者、本文、場所から予定を検索する
* Coding Agentが指定期間の会議・予定を把握できるようにする
* プロジェクトや人物ごとの会議履歴を取得する
* Teams会議、通常会議、個人予定を同じインターフェースで検索する
* 元のOutlook予定へ遷移できる状態を維持する

---

## 3. 取得対象

### 3.1 対象予定表

初期バージョンでは、サインインユーザーが所有する予定表を対象とする。

以下を設定により選択する。

* 既定の予定表
* ユーザーが所有する追加予定表
* ユーザーが参照権限を持つ共有予定表

初期値では既定の予定表だけを有効とする。

```yaml
calendar:
  calendars:
    - id: primary
      enabled: true
```

予定表APIでは、ユーザー予定表やMicrosoft 365グループの既定予定表を取得できる。初期実装ではグループ予定表を対象外とする。

### 3.2 取得期間

予定表は無期限に増加するため、同期対象期間を設定する。

初期値:

* 過去: 3年
* 未来: 1年

```yaml
calendar:
  range:
    past_days: 1095
    future_days: 365
```

期間は日単位または日時で指定可能とする。

### 3.3 対象イベント

* 通常の予定
* 会議
* Teams会議
* 終日予定
* 定期予定
* 定期予定の個別インスタンス
* 定期予定の例外
* キャンセル済み予定
* 仮承諾の会議
* 非公開予定

### 3.4 非公開予定

非公開予定について、Graph APIから取得可能な範囲のみ保存する。

初期方針:

* 件名や本文が取得できない場合は推測しない
* `sensitivity=private`を保存する
* 取得できた本文であってもMCP出力を制限可能にする
* 設定により件名と本文をマスクできる

```yaml
calendar:
  private_events:
    store_details: false
    expose_to_mcp: false
```

---

## 4. 対象データ

イベントごとに以下を取得する。

* GraphイベントID
* iCalUId
* Series Master ID
* イベント種別
* 件名
* 本文
* 本文プレビュー
* 開始日時
* 終了日時
* タイムゾーン
* 終日予定
* 場所
* 複数場所
* 主催者
* 出席者
* 出席回答
* 自分の回答状態
* Teams会議URL
* オンライン会議情報
* Web URL
* リマインダー
* 空き時間状態
* 重要度
* 秘密度
* カテゴリ
* キャンセル状態
* 作成日時
* 更新日時
* 添付ファイル情報
* 拡張プロパティ
* 元のGraph JSON

Microsoft GraphのcalendarViewは、指定期間内の単発イベント、定期予定の発生、例外を取得できる。

---

## 5. 対象外

初期バージョンでは以下を対象外とする。

* 予定の作成
* 予定の更新
* 予定の削除
* 会議出席依頼への回答
* 会議の転送
* 出席者へのメール送信
* 空き時間検索
* 会議室予約
* 添付ファイル本体の保存
* Teams会議の録画・文字起こし取得
* Web管理画面
* ベクトル検索
* Microsoft 365グループ予定表

---

## 6. 技術構成

| 項目     | 採用技術                         |
| ------ | ---------------------------- |
| 実装言語   | Go                           |
| データベース | SQLite                       |
| 全文検索   | SQLite FTS5またはN-gram         |
| 予定表API | Microsoft Graph API          |
| 認証     | Microsoft Entra ID OAuth 2.0 |
| 設定形式   | YAML                         |
| MCP    | Go製MCPサーバー                   |
| 配布形式   | 単一バイナリ                       |
| 動作形態   | CLI、Daemon、MCP               |

---

## 7. システム構成

```text
Outlook Calendar
    ↓
Microsoft Graph API
    ↓
Calendar Sync Service
    ├─ 予定表一覧取得
    ├─ calendarView差分取得
    ├─ 定期予定展開
    ├─ 参加者情報取得
    ├─ Teams会議情報取得
    └─ 更新・削除反映
         ↓
SQLite
    ├─ 予定表
    ├─ イベント
    ├─ 参加者
    ├─ 場所
    ├─ カテゴリ
    ├─ 添付情報
    ├─ 同期期間
    ├─ 同期状態
    └─ 検索インデックス
         ↓
Outlook Calendar MCP
         ↓
Claude Code / Codex
```

---

## 8. 基本方針

### 8.1 Graph APIとMCPを分離する

MCPはSQLiteだけを参照する。

最新予定が必要な場合は同期を実行する。

### 8.2 calendarViewを基本とする

単純なイベント一覧ではなく、期間指定の`calendarView`を基本取得APIとする。

理由:

* 定期予定を実際の発生単位で取得できる
* 単発予定、例外、変更された発生を同一形式で扱える
* MCP利用時に実際の日付ごとの予定を返しやすい

予定表のイベント一覧APIでは、単発予定と定期予定のマスターが返る。展開済みの発生を取得するにはcalendarViewまたはinstancesが必要となる。

### 8.3 期間を分割して同期する

過去3年から未来1年を単一同期単位にしない。

月単位または四半期単位に同期ウィンドウを分割する。

推奨初期値:

* 過去期間: 3か月単位
* 直近未来: 1か月単位
* 遠い未来: 3か月単位

これにより以下を実現する。

* 再同期範囲の限定
* delta token失効時の影響縮小
* APIページング量の抑制
* エラー分離

### 8.4 Outlookを正とする

SQLiteは検索用コピーとする。

イベントにはOutlook Web URLを保存する。

Teams会議の場合はTeams会議URLも保存する。

### 8.5 ローカル時刻とUTCを両方管理する

DBでは日時をUTCへ正規化して保存し、元のタイムゾーンも保持する。

表示時は設定されたタイムゾーンへ変換する。

初期値:

```yaml
calendar:
  display_timezone: Asia/Tokyo
```

---

## 9. 実行モード

### 9.1 CLI

```text
outlook calendar auth login
outlook calendar auth status

outlook calendar list
outlook calendar show CALENDAR_ID

outlook calendar sync
outlook calendar sync --calendar CALENDAR_ID
outlook calendar sync --from 2026-01-01 --to 2026-12-31
outlook calendar sync --full

outlook calendar search "工事改善"
outlook calendar day 2026-07-10
outlook calendar range 2026-07-01 2026-07-31
outlook calendar status
```

### 9.2 Daemon

```text
outlook calendar daemon
```

実行内容:

* 予定表一覧更新
* 同期ウィンドウ生成
* calendarView差分同期
* 予定のUPSERT
* キャンセル・削除反映
* 検索インデックス更新
* 過去期間の定期再照合
* 未来期間の延長

### 9.3 MCP

```text
outlook calendar mcp
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
* `Calendars.Read`
* `offline_access`

予定への変更権限は要求しない。

共有予定表へのアクセスが必要な場合は、Graph権限とOutlook側の予定表共有権限の双方を前提とする。

### 10.3 トークン保存

メール同期と同じ資格情報ストアを共用する。

---

## 11. 同期方式

### 11.1 予定表一覧

起動時および定期的に予定表一覧を取得する。

以下を保存する。

* Calendar ID
* 名前
* 所有者
* 色
* 既定予定表か
* 変更可能か
* 共有可能か
* 使用可能か

### 11.2 calendarView delta

各予定表と同期ウィンドウの組み合わせごとにdelta linkを保存する。

```text
calendar
    └─ sync window
         ├─ start
         ├─ end
         ├─ @odata.nextLink
         └─ @odata.deltaLink
```

calendarViewのdelta queryは、指定期間内の追加、更新、削除を増分取得できる。初回呼び出しは完全同期、その後はdelta linkを使った増分同期となる。

### 11.3 同期ウィンドウ

例:

```text
2023-07-01 ～ 2023-09-30
2023-10-01 ～ 2023-12-31
...
2026-07-01 ～ 2026-07-31
2026-08-01 ～ 2026-08-31
...
2027-04-01 ～ 2027-06-30
```

ウィンドウ境界では重複を避けるため、半開区間として管理する。

```text
[start, end)
```

イベントIDと発生日時でUPSERTする。

### 11.4 未来期間の延長

Daemon実行時に、未来の同期済み範囲が設定値を下回った場合、新しい同期ウィンドウを作成する。

### 11.5 過去期間の削除

設定期間より古くなったイベントは以下から選択する。

* DBに保持する
* 本文だけ削除する
* イベント全体を削除する

初期値ではDBに保持する。

### 11.6 定期予定

以下を区別して保存する。

* `singleInstance`
* `occurrence`
* `exception`
* `seriesMaster`

実際の日時検索では`occurrence`、`exception`、`singleInstance`を使用する。

`seriesMaster`は定期予定の構造確認と関連付けのために保存する。

### 11.7 キャンセルと削除

キャンセル済みイベント:

* `is_cancelled = 1`
* 検索条件で除外可能
* 履歴として保持

削除通知:

* `deleted_at`を設定
* 本文、場所、会議URLを削除可能
* 検索インデックスから除外

### 11.8 レート制限

HTTP 429では`Retry-After`を尊重する。

予定表と同期ウィンドウ単位でエラーを分離する。

---

## 12. データモデル

### 12.1 calendars

```sql
CREATE TABLE calendars (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    owner_name TEXT,
    owner_address TEXT,
    color TEXT,
    hex_color TEXT,
    is_default INTEGER NOT NULL DEFAULT 0,
    can_edit INTEGER,
    can_share INTEGER,
    can_view_private_items INTEGER,
    enabled INTEGER NOT NULL DEFAULT 1,
    raw_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

### 12.2 events

```sql
CREATE TABLE calendar_events (
    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL,
    calendar_id TEXT NOT NULL,

    ical_uid TEXT,
    series_master_id TEXT,
    original_start TEXT,
    event_type TEXT,

    subject TEXT,
    body_html TEXT,
    body_text TEXT,
    body_preview TEXT,
    body_content_type TEXT,

    start_utc TEXT NOT NULL,
    end_utc TEXT NOT NULL,
    start_timezone TEXT,
    end_timezone TEXT,
    is_all_day INTEGER NOT NULL DEFAULT 0,

    organizer_name TEXT,
    organizer_address TEXT,

    location_name TEXT,
    location_uri TEXT,
    location_type TEXT,

    online_meeting_provider TEXT,
    online_meeting_url TEXT,
    join_url TEXT,

    web_url TEXT,
    show_as TEXT,
    importance TEXT,
    sensitivity TEXT,

    response_status TEXT,
    response_time TEXT,

    is_cancelled INTEGER NOT NULL DEFAULT 0,
    is_organizer INTEGER,
    is_online_meeting INTEGER,
    has_attachments INTEGER,

    reminder_minutes INTEGER,
    reminder_enabled INTEGER,

    created_at TEXT,
    modified_at TEXT,
    deleted_at TEXT,

    etag TEXT,
    content_hash TEXT,
    raw_json TEXT NOT NULL,
    indexed_at TEXT,

    UNIQUE(calendar_id, id),
    FOREIGN KEY(calendar_id) REFERENCES calendars(id)
);
```

### 12.3 attendees

```sql
CREATE TABLE calendar_attendees (
    event_row_id INTEGER NOT NULL,
    address TEXT NOT NULL,
    display_name TEXT,
    attendee_type TEXT,
    response TEXT,
    response_time TEXT,
    proposed_start_utc TEXT,
    proposed_end_utc TEXT,
    PRIMARY KEY(event_row_id, address),
    FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id)
);
```

`attendee_type`:

* `required`
* `optional`
* `resource`

### 12.4 locations

```sql
CREATE TABLE calendar_locations (
    event_row_id INTEGER NOT NULL,
    display_name TEXT,
    location_uri TEXT,
    location_type TEXT,
    unique_id TEXT,
    unique_id_type TEXT,
    address_json TEXT,
    coordinates_json TEXT,
    FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id)
);
```

### 12.5 categories

```sql
CREATE TABLE calendar_categories (
    event_row_id INTEGER NOT NULL,
    category TEXT NOT NULL,
    PRIMARY KEY(event_row_id, category),
    FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id)
);
```

### 12.6 attachments

```sql
CREATE TABLE calendar_attachments (
    id TEXT NOT NULL,
    event_row_id INTEGER NOT NULL,
    name TEXT,
    content_type TEXT,
    size INTEGER,
    is_inline INTEGER NOT NULL DEFAULT 0,
    attachment_type TEXT,
    raw_json TEXT,
    PRIMARY KEY(event_row_id, id),
    FOREIGN KEY(event_row_id) REFERENCES calendar_events(row_id)
);
```

### 12.7 sync_windows

```sql
CREATE TABLE calendar_sync_windows (
    calendar_id TEXT NOT NULL,
    window_start_utc TEXT NOT NULL,
    window_end_utc TEXT NOT NULL,
    next_link TEXT,
    delta_link TEXT,
    last_attempt_at TEXT,
    last_success_at TEXT,
    last_full_sync_at TEXT,
    last_error TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(calendar_id, window_start_utc, window_end_utc),
    FOREIGN KEY(calendar_id) REFERENCES calendars(id)
);
```

---

## 13. 本文処理

予定本文について以下を保存する。

* HTML本文
* プレーンテキスト本文
* 本文プレビュー

変換処理:

* HTMLタグ除去
* 改行維持
* URL保持
* Teams会議情報の抽出
* 電話会議情報の抽出
* 不要な空白の整理

会議参加URLは本文から推測するだけでなく、Graphのオンライン会議情報を優先する。

---

## 14. タイムゾーン

### 14.1 保存

以下を保存する。

* UTC開始日時
* UTC終了日時
* 元の開始タイムゾーン
* 元の終了タイムゾーン

### 14.2 表示

MCPおよびCLIでは設定された表示タイムゾーンを使用する。

初期値:

```yaml
calendar:
  display_timezone: Asia/Tokyo
```

### 14.3 終日予定

終日予定はローカル日付境界を維持する。

単純にUTCへ変換して日付を表示しない。

---

## 15. 検索仕様

### 15.1 検索対象

* 件名
* 本文
* 主催者
* 出席者
* 場所
* Teams会議URL
* カテゴリ
* 予定表名

### 15.2 検索条件

* キーワード
* 開始日時
* 終了日時
* 予定表
* 主催者
* 出席者
* 場所
* Teams会議のみ
* 終日予定のみ
* 自分が主催者
* 自分が出席者
* 回答状態
* カテゴリ
* キャンセル済みを含むか
* 非公開予定を含むか
* 最大件数

### 15.3 検索結果

* イベントID
* 件名
* 開始日時
* 終了日時
* 主催者
* 主な出席者
* 場所
* Teams会議URL
* Outlook URL
* 本文抜粋
* 回答状態
* 予定表名

---

## 16. MCP仕様

### 16.1 search_events

```json
{
  "query": "工事改善",
  "from": "2026-06-01T00:00:00+09:00",
  "to": "2026-07-31T23:59:59+09:00",
  "attendees": ["山下"],
  "online_meeting_only": false,
  "limit": 20
}
```

### 16.2 get_event

指定イベントの詳細を取得する。

入力:

* `event_id`
* `calendar_id`

### 16.3 get_schedule

指定期間の予定一覧を返す。

入力:

* `from`
* `to`
* `calendars`
* `include_cancelled`
* `include_private`

### 16.4 get_day_schedule

指定日の予定を時系列で返す。

入力:

* `date`
* `timezone`

### 16.5 get_upcoming_events

今後の予定を返す。

入力:

* `duration`
* `limit`

### 16.6 find_events_by_participant

指定ユーザーが主催または参加する予定を検索する。

### 16.7 find_events_by_project

件名、本文、参加者、場所からプロジェクト関連会議を検索する。

LLMによる最終的な関連性判断はMCP利用側で行う。

### 16.8 get_meeting_history

指定した参加者または検索語に関連する過去の会議を時系列で返す。

### 16.9 get_event_context

指定イベントの前後に開催された関連イベントを取得する。

### 16.10 open_in_outlook

Outlook Web URLを返す。

### 16.11 get_join_url

Teams等のオンライン会議参加URLを返す。

---

## 17. 設定ファイル

```yaml
database:
  path: ./data/outlook-knowledge.db

entra:
  tenant_id: ${OUTLOOK_TENANT_ID}
  client_id: ${OUTLOOK_CLIENT_ID}

calendar:
  display_timezone: Asia/Tokyo

  range:
    past_days: 1095
    future_days: 365

  calendars:
    - id: primary
      enabled: true

  private_events:
    store_details: false
    expose_to_mcp: false

  sync_windows:
    recent_months_per_window: 1
    historical_months_per_window: 3

sync:
  interval: 5m
  calendar_full_resync_interval: 168h
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
outlook calendar list
outlook calendar show CALENDAR_ID

outlook calendar sync
outlook calendar sync --calendar CALENDAR_ID
outlook calendar sync --from 2026-01-01 --to 2026-12-31
outlook calendar sync --full

outlook calendar day 2026-07-10
outlook calendar range 2026-07-01 2026-07-31
outlook calendar search "工事改善"
outlook calendar show-event EVENT_ID
outlook calendar status
```

---

## 19. 非機能要件

### 性能

初期目標:

* 100万イベントまで単一SQLiteで動作する
* 指定日の予定取得を1秒以内に返す
* キーワード検索を2秒以内に返す
* 同期中もMCP検索を継続できる

### セキュリティ

* DBファイルのアクセス権を実行ユーザーに限定する
* MCPを外部公開しない
* トークンを平文保存しない
* 非公開予定の詳細を設定に従ってマスクする
* 予定本文をログへ出力しない
* 削除済み予定の本文を保持しない
* HTML本文を実行しない

### 再実行性

同一deltaページを複数回処理しても重複しない。

### バックアップ

SQLite Backup APIを使用する。

---

## 20. SQLite設定

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
```

同期サービスだけが書き込みを行い、MCPは読み取り専用接続を使用する。

---

## 21. テスト方針

### 単体テスト

* calendarViewレスポンス変換
* タイムゾーン変換
* 終日予定
* 定期予定
* 例外予定
* キャンセル
* delta link管理
* 参加者情報
* Teams会議URL抽出
* 非公開予定マスク
* N-gram検索

### 結合テスト

* 複数ページ
* HTTP 429
* HTTP 401
* HTTP 403
* delta token失効
* 削除通知
* 定期予定変更
* 単一発生の変更
* 会議キャンセル
* 予定表権限不足

### 実環境テスト

* 個人予定
* Teams会議
* 終日予定
* 定期予定
* 定期予定の例外
* 非公開予定
* 共有予定表
* Outlook URL
* Claude Code MCP
* Codex MCP

---

## 22. 実装フェーズ

### Phase 1: Calendar CLI

* Device Code Flow
* 予定表一覧
* calendarView取得
* SQLite保存
* CLI日付検索
* Outlook URL取得

### Phase 2: 差分同期

* calendarView delta
* 同期ウィンドウ
* delta link保存
* キャンセル・削除反映
* Daemon

### Phase 3: Calendar MCP

* `search_events`
* `get_event`
* `get_schedule`
* `get_day_schedule`
* `get_upcoming_events`
* `find_events_by_participant`
* `open_in_outlook`
* `get_join_url`

### Phase 4: 検索改善

* プロジェクト別検索
* 会議履歴
* 関連イベント
* 会議とメールの関連付け
* Teams会話との関連付け

---

## 23. 完了条件

* サインインユーザーの予定表を一覧取得できる
* 同期対象予定表を選択できる
* 同期対象期間を設定できる
* 単発予定を取得できる
* 定期予定の発生と例外を取得できる
* 終日予定を正しく扱える
* UTCと元タイムゾーンを保持できる
* 予定をSQLiteへ保存できる
* calendarViewの差分同期ができる
* 更新、キャンセル、削除を反映できる
* 非公開予定を設定に従ってマスクできる
* 件名、本文、参加者、場所を検索できる
* 指定日の予定を取得できる
* MCPから予定を検索できる
* Outlook原文URLを返せる
* Teams会議参加URLを返せる
* トークンを平文保存しない

