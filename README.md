# mysql-in-go

**MySQL 8.4 LTS を Go の実行ファイルに組み込んで動かす実験です。**
[Bao](https://github.com/moriyoshi/bao) を使って MySQL のコンパイル済みコードをリンクし、
`CGO_ENABLED=0` でビルドした Go プログラムからサーバー本体を呼び出します。

```text
サーバー: bin/mysqlmem（1 プロセス）
┌──────────────────────────────────────────┐
│ Go の main()                             │
│   → Bao の呼び出し口                      │
│     → MySQL の mysqld_main()              │
│       → InnoDB、接続受付、SQL の実行       │
└──────────────────────┬───────────────────┘
                       │ Unix ソケット
クライアント:          │
bin/selectone ─────────┘
（Go の MySQL ドライバー、別プロセス）
```

サーバー側が外部の `mysqld` コマンドを起動することはありません。
MySQL の C++ を Go に翻訳するのではなく、C++ から作った機械語と Go を同じ実行ファイルへ入れています。
ビルド時には C/C++ コンパイラーが必要です。実行時にも glibc や OpenSSL の共有ライブラリを使います。

名前の `mem` はインメモリ専用という意味ではありません。InnoDB はデータディレクトリに保存し、
再起動後にもデータが残ることを確認しています。

## できたこと

```text
MySQL 8.4.13: SELECT 1 = 1
```

テーブル作成、読み書き、JOIN、集計、トランザクション、JSON、ウィンドウ関数、
制約エラー、正常終了後の永続化まで、期待値と比較して確認済みです。
[確認項目と結果](docs/verification.md)を参照してください。

今回の測定では、新規 DB の `Start()` は平均 **1.72 秒**、初期化済みの空 DB なら **0.36 秒**。
Start → SELECT 1 → Close の全体は平均 **2.75 秒**でした。
[測定条件と再実行方法](docs/startup-timing.md)を参照してください。

同じホストで Testcontainers＋Podman と比較したところ、新規 DB の接続可能までの平均は
両方のデータを tmpfs に揃えた最新の通常終了条件では、こちらが **1.72 秒**、
Testcontainers 側が **5.15 秒**でした。
[比較の条件・生データ・再実行手順](benchmarks/testcontainers/README.md)も公開しています。

`mysql` パッケージの `Start()` / `Query()` / `Close()` から利用できます。
内部では **MySQL を内蔵した専用の Go 実行ファイルを子プロセスとして起動**します。
`Close()` で子を終了しても、呼び出し元の Go プログラムは動き続けます。
同一プロセス内で MySQL を起動・停止する API ではありません。
付属サーバーは親パイプの EOF を監視し、親が異常終了した場合は強制終了します。
その場合、正常停止や残ったデータの削除までは保証しません。
全機能、長時間運用、クラッシュリカバリーは未検証です。

## Go から起動・終了する

ビルド済みのリポジトリのルートで実行できます。

```sh
make example
```

```text
SELECT 1 = 1
MySQL を終了しました。Go の処理は続いています。
```

[実行できるサンプル](cmd/example/main.go)では次の API を使います。

```go
db, err := mysql.Start()
if err != nil {
    return err
}
defer db.Close()

rows, err := db.Query("SELECT 1")
// err の確認、行の読み取り、rows.Close() を行う

if err := db.Close(); err != nil {
    return err
}
// ここからも親の Go プログラムは動き続ける
```

```text
親の Go: Start() → Query() → Close() → Go の処理を続行
           │         │         │
           │ 起動    │ SQL     │ SHUTDOWN と終了待ち
           ▼         ▼         ▼
子: bin/mysqlmem（Bao で MySQL を組み込んだ実行ファイル）
```

SQL プールは非公開です。必要な場合だけ `db.Unwrap()` で `*sql.DB` を取得できます。
新規 DB の指定は `mysql.Options{Database: "test", CreateDatabase: true}`、
終了方式の選択は `FastShutdown: true`（次回起動時の crash recovery が必要）を使います。
fixture の baseline は引き続き 0 で停止します。

デフォルトのデータは一時的で、正常な `Close()` 時に削除します。
保存したい場合は `mysql.Start(mysql.Options{DataDir: "./my-data"})` と指定します。
[API の設定・寿命・制約](docs/api.md)に詳しい説明があります。

## Prepare once / テストごとの DB

`mysqltest.New(..., Options{Prepare: ...})` で初期データを一度準備し、
`fixture.DB(t)` でテスト専用の `*sql.DB` を受け取れるようになりました。
テストの変更は別テストに漏れず、子プロセスは Cleanup で終了します。
`auto.cnf` は複製せず、各 child は新しい `server_uuid` を生成します。

```sh
make test-fixture
```

mariamem のメモリー Snapshot とは異なり、停止済みデータのコピーから起動する試作です。
小さな seed での初回測定では、子のコピー＋起動は約 0.41 秒、終了まで約 1.41 秒でした。
[使い方・所有権・測定条件](docs/fixture.md) / [実行可能なテスト](examples/fixture/fixture_test.go)

## tmpfs 上で使い捨てる

Linux では `mysql.Options{Tmpfs: true}` を指定すると、検証済みの `/dev/shm` tmpfs に
データを置けます。fixture の基準データとコピーにも使えます。

```sh
make test-fixture-tmpfs
```

[使い方と実測結果](docs/tmpfs.md)を参照してください。今回のホストは元々 `/tmp` も tmpfs だったため、
明示的な tmpfs モードでの大幅な速度改善はありませんでした。

## 対象環境

動作確認した環境は Ubuntu 26.10（stonking）/ Linux amd64 です。
現在のスクリプトはこの環境の apt パッケージとライブラリ配置を使います。
macOS、Windows、arm64 や別の Linux での動作は未確認です。

| 入力 | 固定バージョン |
| --- | --- |
| MySQL | 8.4.13 / `8a180268d832300c09a375115dfa0ea77d83f1af` |
| Bao | `18c47c82d494c5a7fff7b4ecebd02a98c728d7a5` ＋同梱パッチ |
| Go | 1.26.10 |

ホストに Git、curl、Python 3、CMake、Ninja、GNU C/C++、binutils、make、apt、dpkg-deb と、
C/C++ の標準開発ヘッダー、zlib/ncurses 開発パッケージ、`libssl.so.3` / `libcrypto.so.3` が必要です。
検証時のコンパイラーは GCC 15.3.0、glibc は 2.44 です。
初回は MySQL 本体をソースからビルドするため時間がかかります。並列数はメモリーに合わせて調整してください。

## ビルドして試す

リポジトリのルートで実行します。初回のソース・依存取得にはネットワークが必要です。
Go、Bison、m4、rpcgen、OpenSSL 3 ヘッダーは `.work/` に展開します。

```sh
make bootstrap
make configure
make mysql JOBS=10
make package
make build

# 初期化 → 起動 → SELECT 1 → SQL SHUTDOWN
make test

# SQL の機能確認 → 正常終了 → 再起動 → 永続化確認
make test-sql

# Go API の起動・終了・永続化・異常系
make test-api

# 通常の Go テストで SELECT 1 の結果を assert
make test-select-one
```

テストは所有者だけがアクセスできる一時ディレクトリ `.work/probe-*` / `.work/sql-*` を作り、
パスワードなしの検証用 root と Unix ソケットを使用します。TCP は無効です。
ログとデータはそのディレクトリに残ります。既存の MySQL や既存データディレクトリは使用しません。

起動済みの検証サーバーへクエリーだけ送る場合は、表示されたソケットを指定します。

```sh
MYSQL_DSN='root@unix(/path/to/mysql.sock)/' bin/selectone
```

`bin/mysqlmem` の動作にはメッセージファイルなども必要です。実行ファイル単体をコピーする配布形態は整えていません。
[ビルドの仕組みと必要な調整](docs/build-notes.md)に詳細をまとめています。

## ソースの構成とチェック

| 場所 | 役割 |
| --- | --- |
| `mysqltest/` | Prepare once と独立したテスト DB の fixture |
| `examples/fixture/` | fixture の利用テスト |
| `mysql/` | 子プロセスを管理する Start / Close API とテスト |
| `cmd/example/` | Go API を利用するサンプル |
| `cmd/mysqlmem/` | Bao 経由で MySQL サーバー本体を呼ぶ入口 |
| `cmd/selectone/` | Go からの接続確認 |
| `cmd/sqlcheck/` | SQL の結果とエラーを検証 |
| `scripts/` | 取得、ビルド、リンク、実行テスト、上流への差分 |
| `signatures.json` | Bao に公開する C ABI の宣言 |
| `.work/`、`native/`、`bin/` | 取得物と生成物。Git 管理対象外 |

```sh
make check-source  # MySQL/Bao 生成物なしでも実行可能
make fmt
make vet           # native/ 生成後のサーバー側も含む静的解析
make source-archive
```

GitHub Actions は `check-source` の軽量チェックを実行します。
MySQL のビルドと SQL テストは、上記の対象環境で実行する統合検証です。

`source-archive` は公開用ソースのみを `dist/mysqlmem-source.tar.gz` にまとめます。
取得済みの MySQL、生成されたネイティブコード、実行ファイル、DB データ、環境設定は含めません。

## ライセンス

このプロジェクトの自作コードは [MIT License](LICENSE) です。
同梱する MySQL / Bao 向けの差分は元のライセンスに従います。
第三者のソースや生成バイナリーの扱いは [THIRD_PARTY.md](THIRD_PARTY.md) を参照してください。
