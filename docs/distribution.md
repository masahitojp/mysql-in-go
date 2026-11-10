# Go ライブラリとビルド済みサーバーの配布

初期配布の対象は Linux amd64、Ubuntu 24.04 / 26.04。
サーバーは Ubuntu 24.04 の glibc 2.39 / OpenSSL 3.0.13 でビルドし、
OS の glibc と libssl.so.3 / libcrypto.so.3 に動的リンクする。
macOS / ARM64 向けバンドルはまだ提供しない。

```sh
sudo apt-get update
sudo apt-get install -y libssl3t64
go get github.com/masahitojp/mysql-in-go@v0.1.0
```

```go
package main

import (
    "fmt"
    mysql "github.com/masahitojp/mysql-in-go"
)

func main() {
    db, err := mysql.Start()
    if err != nil { panic(err) }
    defer db.Close()
    var one int
    if err := db.QueryRow("SELECT 1").Scan(&one); err != nil { panic(err) }
    fmt.Println(one)
}
```

`go get` は Go API を取得する。MySQL のビルドは利用者側では行わない。
`Start` は固定した Release のサーバーバンドルを初回ダウンロードし、
Go モジュールに埋め込んだ SHA-256 と照合してから展開する。
OSパッケージの自動インストールや sudo の実行はしない。

## キャッシュとオフライン

保存先は `os.UserCacheDir()/mysql-in-go/<version>/<os-arch>/<sha256>`。
Linux では通常 `~/.cache`、XDG_CACHE_HOME が指定されていればその配下。
同時呼び出しは一時ディレクトリに別々に取得し、検証した完成品を最後に公開する。
不正なパス、symlink、特殊ファイル、サイズ超過、チェックサム不一致は拒否する。
キャッシュは同じユーザーだけが管理する前提。初回取得にはネットワークが必要。

```go
// CI イメージ作成時などに、サーバーを起動せず事前取得する。
err := mysql.Prefetch(ctx)
```

以降はキャッシュがあればオフラインでも起動できる。完全に手動配置する場合は
Options.Binary / BaseDir / CharacterSetsDir の3つを指定する。
ソースチェックアウト内では既存の bin/mysqlmem と .work 内のファイルを優先するため、
ローカルの開発用ビルドも継続できる。

OpenSSL の更新は OS のパッケージ更新で適用する。
同じ .so.3 名だけを見て互換性を判断せず、配布側で最古の対象OSを使ってビルドし、
対応OS上の初期化・SQL・fixture・終了を確認する。
Ubuntu 24.04/26.04 以外のディストリビューションを動作保証するものではない。

## 使い捨てテスト

`mysqltest.New(ctx, mysqltest.Options{MySQL: mysql.Options{Tmpfs: true}, DiscardChildren: true})`
も同じバンドルを使う。/dev/shm の空き容量が必要。
Podman/Docker のデフォルト64MiBでは小さいため、テスト用には例えば --shm-size=1g を設定する。
一般的なホスト上で動かす場合、コンテナエンジン自体は不要。

## サーバーとソース

Go API は MIT。MySQL を含むサーバーは root の MIT ライセンスに変更されない。
配布物には MySQL・Bao bootstrap・Go・GCC・glibc の必要な表示を含め、
対応するソース、変更、生成・ビルドスクリプトを同じ Release で提供する。
サーバーの MySQL ソースは Git リポジトリに混ぜず、Release の別アセットに置く。
公開 API とサーバーのビルドを分離するため、server/ は独立した Go モジュール。

`internal/serverbundle/manifest.json` が利用するサーバー版・URL・SHA-256 を固定する。
サーバー更新時は新しいバンドルと対応ソースを作り、manifest を更新して API をリリースする。

## 配布者のビルド

Linux amd64 の開発環境で Podman・curl・git・Python 3・tar を用意し、
`make release-build` を実行する。コンパイラーと OpenSSL の開発用ファイルは
build/ubuntu24.Dockerfile の中だけに入れる。通常の利用者には不要。

`scripts/release-bundle.py` は実行ファイル、errmsg、文字セット、ICU データ、表示を梱包する。
`scripts/corresponding-source.py` は MySQL / Bao / Go と Ubuntu の GCC・glibc ソースを含む
別アーカイブを作る。ソースパッケージはビルドした版と一致させる。
このアーカイブは `make source-archive` の軽量なプロジェクト用アーカイブとは別物。
`make release-source` で同じビルダーの版に対応するソースを取得し、アーカイブにまとめる。
