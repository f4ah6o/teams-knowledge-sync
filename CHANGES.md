# Changes

## Unreleased

### Added

- Outlook予定表を指定期間で初回同期し、日付・期間・キーワード検索と詳細表示ができる `outlook-knowledge calendar` CLIを追加した。(`issues/done/20260710-add-calendar-cli-sync.md`)
- 登録アドレスに関係するOutlookメールを指定フォルダーから初回同期し、検索・詳細表示・スレッド表示できる `outlook-knowledge mail` CLIを追加した。(`issues/done/20260710-add-mail-cli-sync.md`)

### Changed

- Outlook Calendar同期が固定ウィンドウ単位のdeltaLink、削除反映、token失効時の局所復旧、未来範囲の自動延長に対応した。(`issues/done/20260710-add-calendar-delta-sync.md`)
- Outlook Mail同期がフォルダー単位のdeltaLinkを利用し、追加・更新・削除・移動の反映と定期daemon実行に対応した。(`issues/done/20260710-add-mail-delta-sync.md`)
- Teamsのチャネル・チャット同期が、コンテナ単位の最終成功時刻と24時間のオーバーラップを利用するようになった。(`issues/done/20260710-add-teams-overlap-sync.md`)

### Fixed

### Deprecated

### Removed

### Security

### Migration
