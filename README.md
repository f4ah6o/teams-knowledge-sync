# Teams Knowledge Sync

Microsoft Teamsの指定チームと参加チャットをローカルSQLiteへ同期し、CLIとstdio MCPで検索するGoバイナリです。

Outlook Mailは別の `outlook-knowledge` バイナリで、登録アドレスに関係するメールを指定フォルダーからローカルSQLiteへ同期し、CLI検索できます。初回取得後はフォルダー単位のdeltaLinkで変更を追跡します。

## Setup

1. Entra IDで公開クライアントアプリを作成し、`User.Read`、`Team.ReadBasic.All`、`Channel.ReadBasic.All`、`ChannelMessage.Read.All`、`Chat.Read`の委任権限に同意します。
2. `config.example.yaml`を`config.yaml`へコピーし、tenant/client IDを設定します。
3. Cloudflare Tunnelで`teams-knowledge.obr-grp.com`を`http://127.0.0.1:8787`へ転送します。Tunnelはこのアプリとは別プロセスで起動し、通知以外は404へルーティングしてください。
4. `go run ./cmd/teams auth login`、続いて`go run ./cmd/teams sync all`を実行します。

Outlook Mailは `outlook-config.example.yaml` を `config.yaml` にコピーして登録アドレスを設定し、Entra IDアプリへ `User.Read` と `Mail.Read` の委任権限を追加します。その後、`go run ./cmd/outlook-knowledge mail auth login` と `go run ./cmd/outlook-knowledge mail sync` を実行します。

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

outlook-knowledge mail auth login|status|logout
outlook-knowledge mail address list
outlook-knowledge mail folder list
outlook-knowledge mail sync [--folder FOLDER_ID]
outlook-knowledge mail search "工事引継" [--address ADDRESS]
outlook-knowledge mail show MESSAGE_ID
outlook-knowledge mail thread MESSAGE_ID
outlook-knowledge mail status [--json]
outlook-knowledge mail daemon
```

`daemon`は `POST /graph/notifications` だけを受け付け、Graph validation tokenとclientStateを検証して通知をSQLiteキューへ保存します。
