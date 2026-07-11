# Teams Knowledge Sync

Microsoft Teamsの指定チームと参加チャットをローカルSQLiteへ同期し、CLIとstdio MCPで検索するGoバイナリです。

## Setup

1. Entra IDで公開クライアントアプリを作成し、`User.Read`、`Team.ReadBasic.All`、`Channel.ReadBasic.All`、`ChannelMessage.Read.All`、`Chat.Read`の委任権限に同意します。
2. `config.example.yaml`を`config.yaml`へコピーし、tenant/client IDを設定します。
3. Cloudflare Tunnelで`teams-knowledge.obr-grp.com`を`http://127.0.0.1:8787`へ転送します。Tunnelはこのアプリとは別プロセスで起動し、通知以外は404へルーティングしてください。
4. `go run ./cmd/teams auth login`、続いて`go run ./cmd/teams sync all`を実行します。

トークンキャッシュとWebhook秘密鍵はOS資格情報ストアの鍵で保護されます。資格情報ストアを利用できない環境では認証・daemonは起動しません。

## Commands

```
teams auth login|status|logout
teams sync all|team TEAM_ID|chats
teams search "工事引継" --team TEAM_ID
teams message fetch "https://teams.microsoft.com/l/message/..."
teams message fetch "https://teams.microsoft.com/l/message/..." --json
teams status --json
teams daemon
teams mcp
```

`daemon`は `POST /graph/notifications` だけを受け付け、Graph validation tokenとclientStateを検証して通知をSQLiteキューへ保存します。

## Outlook Knowledge

同じリポジトリの`outlook-knowledge`バイナリで、Outlookメールをローカル SQLite（`./data/outlook-knowledge.db`）へ同期しCLIで検索できます。

### Setup

1. Entra IDの公開クライアントアプリに`User.Read`、`Mail.Read`、`Calendars.Read`の委任権限へ同意します（Teams用アプリと同一でも別でも構いません）。
2. `outlook-config.example.yaml`を`outlook-config.yaml`へコピーし、tenant/client IDと登録メールアドレスを設定します。
3. `go run ./cmd/outlook-knowledge auth login`、続いて`go run ./cmd/outlook-knowledge mail sync`を実行します。

登録アドレスは正規化（空白除去・小文字化・表示名/`mailto:`/山括弧の除去）後の完全一致で判定し、受信者・送信者・指定ヘッダー・件名プレフィックスの一致理由を保存します。削除系フォルダー（Deleted Items、Junk、Drafts、Outbox）は取得しません。

### Commands

```
outlook-knowledge auth login|status|logout
outlook-knowledge mail address list
outlook-knowledge mail folder list
outlook-knowledge mail sync [--folder FOLDER_ID] [--full]
outlook-knowledge mail search "工事引継" --address project-ml@example.com
outlook-knowledge mail show MESSAGE_ID
outlook-knowledge mail thread MESSAGE_ID
outlook-knowledge mail status --json
outlook-knowledge daemon
```

同期はフォルダー単位のdeltaリンクで行われ、全ページ反映後だけdelta状態を確定します。deltaトークン失効時は該当フォルダーのみ完全同期へ戻り、`daemon`は`sync.interval`（既定5分）ごとに全対象フォルダーを再同期し、1フォルダーの失敗が他フォルダーを止めません。
