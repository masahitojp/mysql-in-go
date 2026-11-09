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

返り値は SQL プールを非公開で保持し、`Query`、`QueryRow`、`Exec`、
`BeginTx`、それぞれの Context 版などを明示的に転送する。
スキャン先・SQL エラーの扱いは通常の `database/sql` と同じ。

## Close

行の読み取りと `rows.Close()`、トランザクションの Commit / Rollback を済ませてから
`db.Close()` を呼ぶ。処理中のクエリーと並行して Close を呼ぶ利用は想定しない。

`Close()` は SQL の `SHUTDOWN` を送り、接続を閉じ、子プロセスの終了を待つ。
繰り返し呼んでも同じ結果を返し、複数の goroutine が Close を同時に呼ぶこともできる。
成功すれば子は終了コード 0 で終了し、デフォルトの一時データとログは削除される。
親の Go プログラムは継続する。

公開の `db.DB` フィールドはない。ORM などで `*sql.DB` が必要なら `db.Unwrap()` を使う。
Unwrap は明示的な escape hatch で、サーバーの所有権は移さない。必ず所有者の `db.Close()` も呼ぶ。
プールだけが先に閉じられても、所有者の Close は別の管理用接続で MySQL を停止する。
SQL の SHUTDOWN を呼び出し元から直接送らず、この API に終了を任せる。
付属の `bin/mysqlmem` は、API が渡す親監視パイプを MySQL 本体へ入る前に監視する。
親の SIGKILL などで書き込み端が閉じると、子は EOF を検知してプロセス全体を終了コード 125 で終了する。
この監視はデータ初期化用プロセスにも付ける。`Pdeathsig` や起動元 OS スレッドの寿命には依存しない。
正常な Close では SQL SHUTDOWN → Wait → 親側パイプを閉じる順序で、監視による終了と区別する。

EOF 時の停止は強制終了なので、正常停止・データ削除の代わりにはならない。
残ったデータやログは保持し、永続 DB は次回起動時にクラッシュリカバリーが必要になることがある。
この対策を利用するにはサーバーを `make build` で作り直す。
古いバイナリや独自の `Options.Binary` が監視を実装していなければ保護されない。
手動起動の bin/mysqlmem（内部監視 FD の指定なし）は従来どおり動き続ける。
子が SIGSTOP 中など、監視コード自体を実行できない状態での即時終了は保証しない。

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

## デフォルト DB の作成

```go
db, err := mysql.Start(mysql.Options{
    Database:       "test",
    CreateDatabase: true,
})
```

システム接続で起動確認 → `CREATE DATABASE IF NOT EXISTS` → 指定 DB への接続確認、の順に処理する。
識別子はバッククォートを escape して扱う。既存の DB やデータは上書きしない。
`CreateDatabase: true` で Database が空なら、プロセス起動前にエラーにする。
CreateDatabase が false の場合は既存 DB が必要で、不在なら MySQL の 1049 エラーを返して子を終了する。
起動 timeout まで無意味に待機しない。失敗ログは調査用に残す。

## 終了方式

デフォルトは MySQL の `innodb_fast_shutdown=1`。使い捨て DB の高速終了を試す場合は
`mysql.Options{FastShutdown: true}` を指定すると 2 で起動する。
fixture で指定した場合も baseline は明示的に 0 で停止し、コピー元の cold baseline を作る。
子だけが FastShutdown の指定を引き継ぐ。

2 は redo log を flush し、buffer pool の flush などを省略するため、次回起動で crash recovery が必要になる。
初期化コマンドへはこの設定を渡さず、初期化後に起動するサーバーにのみ適用する。
小さい DB の最新測定では Close は通常 1.022 秒、高速 0.999 秒で、大きな改善は確認していない。
[4 条件の比較結果](../benchmarks/testcontainers/README.md)を参照。
`innodb_flush_log_at_trx_commit` などの耐久性設定は変更しない。
公式説明ではコミット済み transaction を失う設定ではないが、復旧・アップグレード・
大きな負荷での性能が通常停止と同じとは扱わない。今回のテストでは small seed の再起動と読み出しを確認した。
[MySQL の説明](https://dev.mysql.com/doc/refman/8.4/en/innodb-parameters.html#sysvar_innodb_fast_shutdown)

サーバーが Close より先に終了した場合、終了コード 0 と異常終了をメッセージで区別する。
`server exited before Close: <nil>` という表示はしない。

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

## UUID と親プロセス死亡の回帰テスト

```sh
make test-lifecycle
```

付属サーバーを再ビルドし、以下を実 MySQL で確認する。

- 2 child の UUID が基準 DB とも互いにも異なり、基準の auto.cnf は変更されない。
- 永続 DB の通常の Close → 再起動では同じ UUID を維持する。
- 起動済み MySQL の親 Go プロセスを SIGKILL し、EOF による子の終了を確認する。
- 初期化中でも同じ EOF 対策が働く。

軽量テストでは、関係ない別の exec が親パイプの書き込み端を引き継がないことも確認する。
親死亡の実測テストは Linux で実行している。Pdeathsig 依存の対策は追加していない。
