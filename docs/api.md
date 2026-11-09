# 子プロセスを管理する Go API

`mysqlmem/mysql` を import して利用する。現在のモジュール名は `mysqlmem` のため、
この例はリポジトリ内で実行する。`go get` 用のモジュールパスへの移行や、
生成済みサーバー・実行用データの自動配布はまだ行っていない。

```go
import "mysqlmem/mysql"
```

## Start と接続

`mysql.Start()` は以下の処理を行い、SQL 接続が成功した時点で戻る。

1. 所有者だけがアクセスできる一時ディレクトリを作る。
2. 新しいデータディレクトリを初期化する。
3. `bin/mysqlmem` を子プロセスとして起動する。
4. Unix ソケットへの接続を確認し、`*mysql.DB` を返す。

TCP 接続は無効。検証用 root はパスワードなしのローカル接続で使用する。
MySQL が呼び出し元と同じプロセス内で動くわけではない。
`Start()` の呼び出しごとに別の子プロセスが起動する。

返り値は `*sql.DB` を埋め込んでいるので、`Query`、`QueryRow`、`Exec`、
`BeginTx`、それぞれの Context 版などを使える。
スキャン先・SQL エラーの扱いは通常の `database/sql` と同じ。

## Close

行の読み取りと `rows.Close()`、トランザクションの Commit / Rollback を済ませてから
`db.Close()` を呼ぶ。処理中のクエリーと並行して Close を呼ぶ利用は想定しない。

`Close()` は SQL の `SHUTDOWN` を送り、接続を閉じ、子プロセスの終了を待つ。
繰り返し呼んでも同じ結果を返し、複数の goroutine が Close を同時に呼ぶこともできる。
成功すれば子は終了コード 0 で終了し、デフォルトの一時データとログは削除される。
親の Go プログラムは継続する。

`db.DB.Close()` は接続プールだけを閉じるため使わず、必ず `db.Close()` を呼ぶ。
SQL の SHUTDOWN を呼び出し元から直接送らず、この API に終了を任せる。
親の異常終了時に子も必ず終了する保証はないので、所有者は Close の実行を管理する。

## tmpfs 上の使い捨てデータ

Linux では `mysql.Start(mysql.Options{Tmpfs: true})` で tmpfs を明示できる。
fixture の基準データ・コピーにも適用できる。詳細は [tmpfs](tmpfs.md) を参照。

## 設定と永続化

省略値はリポジトリのルートをカレントディレクトリにした場合のビルド配置。
別の場所から使う場合はサーバーとデータファイルの場所を明示する。

```go
db, err := mysql.Start(mysql.Options{
    Binary:           "/path/to/mysql-in-go/bin/mysqlmem",
    BaseDir:          "/path/to/mysql-in-go/.work/mysql-build",
    CharacterSetsDir: "/path/to/mysql-in-go/.work/mysql/share/charsets",
    DataDir:          "./my-data",
})
```

`DataDir` は省略すると一時データ。指定したディレクトリは Close で削除しない。
初回は空の場所に新規 DB を作り、以降は同じデータで起動する。
既存の非空ディレクトリに MySQL の `mysql/` ディレクトリがなければ、上書きせずエラーにする。
この API が作成したデータディレクトリを使用し、既存の別サーバーと同じ場所を共有しない。

`StartupTimeout` のデフォルトは 2 分、`ShutdownTimeout` は 30 秒。
`StartContext(ctx, options...)` は初期化と接続待ちをキャンセルできる。
Start 成功後はその ctx のキャンセルでサーバーを止めず、Close で寿命を管理する。

起動失敗や停止タイムアウト時は子を終了・回収し、ログの場所をエラーに含める。
失敗した実行のデータとログは調査用に残す。
停止タイムアウト時の強制終了は正常停止ではなく、Close はエラーを返す。

## 確認方法

```sh
make check-source  # ネイティブサーバー不要の異常系テストを含む
make example       # SELECT 1 と Close 後のメッセージ
make test-api      # 実サーバーの起動・正常終了・永続化を含む
```

起動時の終了・タイムアウト、データ保護、停止タイムアウトの子回収、
実サーバーでの SELECT 1、Close の並行呼び出し、子の回収、一時データ削除、
Close 後の Go 処理継続、保存データを使った再起動を検証する。

## Go のテストで SELECT 1 を assert する

[mariamem の Go テスト構成](https://github.com/masahitojp/mariamem/tree/main/mariamemtest)
のように、通常の `testing.T` から実 DB に接続して結果を比較できる。
実行できる最小例は [`mysql/selectone_integration_test.go`](../mysql/selectone_integration_test.go)。
公開 API だけを使う外部テストパッケージ `mysql_test` で、テストごとに新しい DB を作る。

```sh
make test-select-one
```

テストの中心は次の処理。開始時の `mysql.StartContext` のエラーを確認し、
直後に `t.Cleanup` へ `db.Close()` を登録してから実行する。
Cleanup は assertion が失敗して `t.Fatal` した場合にも呼ばれる。

```go
var got int
if err := db.QueryRowContext(t.Context(), "SELECT 1").Scan(&got); err != nil {
    t.Fatal(err)
}
if got != 1 {
    t.Fatalf("SELECT 1 = %d; want 1", got)
}
```

外部の assertion ライブラリは不要。成功時の出力:

```text
=== RUN   TestSelectOne
    selectone_integration_test.go:…: SELECT 1 = 1 (assertion passed)
--- PASS: TestSelectOne
PASS
```

`make check-source` でもテストコードをコンパイルするが、実 DB の起動はスキップする。
`make test-select-one` は `MYSQLMEM_INTEGRATION=1` を設定し、スキップせず実行する。
`mariamem` のメモリー Snapshot/Fork は実装していない。
停止済みデータのコピーを使う [共有 fixture の試作](fixture.md)は追加している。
ここで確認するのは「テスト用の実 DB を起動 → SQL を assert → Cleanup で終了」という利用形態。
