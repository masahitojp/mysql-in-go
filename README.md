# mysqlmem

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

現在は **MySQL を内蔵した専用の Go 実行ファイル**です。
MySQL がシグナルとプロセス終了も管理するため、任意の Go アプリから自由に起動・停止する API は提供していません。
全機能、長時間運用、クラッシュリカバリーは未検証です。

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
