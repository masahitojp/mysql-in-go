# Prepare once / 独立したテスト DB

`mysqlmem/mysqltest` は mariamem の fixture に近い Go テストの書き方を提供する試作。
`Prepare` は一度だけ実行し、テストごとに停止済みデータの私有コピーと MySQL 子プロセスを作る。
メモリー Snapshot/Fork の実装・互換レイヤーではない。

```text
mysqltest.New
  → MySQL を初期化・起動
  → test DB を作成し Prepare を一度実行
  → 正常終了し、停止したデータを基準として保持

fixture.DB(t)
  → 空き枠を待つ（デフォルト最大 2 台）
  → 基準データを専用ディレクトリへコピー
  → 専用 MySQL を起動
  → *sql.DB を返す
  → t.Cleanup で終了・専用データ削除
```

## 使い方

実行可能な例は [`examples/fixture/fixture_test.go`](../examples/fixture/fixture_test.go)。
先に通常の MySQL/Bao ビルドを済ませ、次で実サーバーを使ったテストを実行する。

```sh
make test-fixture
```

リポジトリのルートから起動する場合の基本形:

```go
f, err := mysqltest.New(t.Context(), mysqltest.Options{
    MaxChildren: 2,
    Prepare: func(ctx context.Context, db *sql.DB) error {
        if _, err := db.ExecContext(ctx,
            "CREATE TABLE items(id INT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
            return err
        }
        _, err := db.ExecContext(ctx, "INSERT INTO items VALUES(1)")
        return err
    },
})
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() {
    if err := f.Close(); err != nil {
        t.Error(err)
    }
})

t.Run("delete", func(t *testing.T) {
    db := f.DB(t)
    if _, err := db.ExecContext(t.Context(), "DELETE FROM items"); err != nil {
        t.Fatal(err)
    }
})
t.Run("seed", func(t *testing.T) {
    var got int
    if err := f.DB(t).QueryRowContext(t.Context(),
        "SELECT COUNT(*) FROM items").Scan(&got); err != nil {
        t.Fatal(err)
    }
    if got != 1 {
        t.Fatalf("got %d; want 1", got)
    }
})
```

Go test の作業ディレクトリは通常パッケージのディレクトリなので、例では
`Options.MySQL` にサーバーと実行用ファイルの絶対パスを渡す。
それらのパスは New 時に固定する。
デフォルト DB 名は `test`。`Options.MySQL.Database` で別名を指定できる。
データディレクトリは fixture が所有するため `Options.MySQL.DataDir` は指定できない。

## 所有権と並列実行

- 同じ fixture で同じ `t.Name()` の `DB(t)` は同じ子を返す。
- 別テストは別の MySQL・データ。コミット済みの変更も相互に漏れない。
- datadir 直下の `auto.cnf` はコピーせず、child の初回起動で `server_uuid` を再生成する。
  基準データの auto.cnf は保持し、通常の永続 DB 再起動では UUID を変えない。
  binlog / GTID の既存履歴はコピー対象のままで、server_id の割り当てやレプリケーション設定は行わない。
  レプリケーション互換性を保証する機能ではない。
- コピーは通常のファイルコピー。ハードリンクや稼働中の DB のコピーは使わない。
  ファイルサイズに応じた I/O とディスク容量が必要。
- MaxChildren（ゼロ時 2）はコピー・起動・稼働・終了中の子の合計上限。
  満杯なら待機し、テストの Context のキャンセルや fixture.Close で待機を解除する。
- テスト側は Rows を閉じ、トランザクションを終了しておく。
  `DB(t)` で受け取った pool の寿命は fixture に任せる。
- 明示的な寿命が必要なら `child, err := f.Open(ctx)` を使い、`child.Close()` で枠を返す。
  Child は SQL プールを埋め込まず、SQL 操作を転送する。raw pool が必要なら `child.Unwrap()` を明示的に使う。
  親テストの cleanup まで子を保持したまま上限以上を要求すると待ち続けるので、
  子を明示的に閉じるかサブテスト単位で `DB(t)` を使う。
- New の Context は準備中のみを管理する。成功後は fixture.Close が所有権を持つ。
- fixture.Close は新規受付・待機中の起動を止め、稼働中の子も終了してから基準データを消す。
  子の終了・削除が失敗した場合は新規受付を止め、エラーと調査用データを保持する。
- Prepare は全 Rows / transaction / worker を完了して戻る。
  Goexit / t.Fatal を Prepare 内で呼ばず、失敗は error で返す。
- Prepare の callback error は基準サーバーを終了して返す。panic は後始末後に再送出する。

## 実測（小さな seed、今回の Linux ホスト）

最初の実行の、5 子を逐次起動した結果:

| 項目 | 所要時間 |
| --- | --- |
| 一度だけの初期化・Prepare・基準サーバー停止 | 2.706 秒 |
| 1 子のデータコピー | 平均 50.7 ms |
| コピー後の起動・接続待ち | 平均 362.8 ms |
| コピー → 起動 → SELECT 1 → 停止・削除 | 平均 1.415 秒 |

起動準備までならコピー＋起動で約 0.41 秒。終了処理が約 1 秒を占める。
Go のビルド時間は含めない。全て一時ディレクトリに置き、OS キャッシュは消去していない。
後の確認で、このホストの `/tmp` は tmpfs と分かったため、この値はディスク上のコピー性能ではない。
明示的な tmpfs モードとその測定は [tmpfs](tmpfs.md) を参照。
これは小さな seed の測定で、大きな基準データでの結果ではない。

検証済み: Prepare 一度のみ、コミットした削除の隔離、同一テストでの pool 再利用、
並列 3 テスト・最大 2 子、上限待機のキャンセル、終了後の子と基準データの削除、
Prepare エラー、fixture 終了後の受付拒否、SELECT 1 の assertion。

mariamem と同じ条件での速度比較はまだ行っていない。
今回の方式は毎回コピーと実 MySQL 起動を行うため、メモリー状態からの Fork と同じ速度は前提にしない。
Live.Reset / 固定接続先の proxy、メモリー Snapshot、永続 Snapshot の API は未実装。

## 高速終了の選択

`Options.MySQL.FastShutdown` を true にすると各 child は innodb_fast_shutdown=2 で起動する。
基準 DB の起動時はこのオプションを無効化し、Prepare 後に 0 を設定して停止するため、
高速停止したデータを baseline にすることはない。
子の raw pool を先に閉じてしまっても、fixture の Cleanup は別の管理接続で子を停止できる。

## tmpfs の子を丸ごと破棄する

```go
f, err := mysqltest.New(ctx, mysqltest.Options{
    MySQL: mysql.Options{Tmpfs: true},
    DiscardChildren: true,
    Prepare: prepare,
})
```

`DiscardChildren: true` では `child.Close()` / `DB(t)` の Cleanup が
子を SIGKILL → Wait で回収 → 専用データを削除する。SQL SHUTDOWN や SQL によるリセットは行わない。
次のテストは毎回、変更していない baseline の新しいコピーから起動する。
このモードは検証済み tmpfs が必須で、指定がなければ New がエラーを返す。
child の FastShutdown 指定は無効化され、innodb_fast_shutdown の値に終了を依存させない。
baseline だけは従来どおり innodb_fast_shutdown=0 で正常停止する。
Rows / transaction / worker はテスト側で片付け、終了後のコピーは再利用しない。

```sh
make test-fixture-discard
```

2026-11-10、同じホスト・小さな seed・各 5 子を逐次実行した平均:

| 項目 | SQL SHUTDOWN（FastShutdown=true、2） | 強制終了・破棄 |
| --- | --- | --- |
| データコピー | 49.1 ms | 44.3 ms |
| 起動・接続待ち | 384.3 ms | 403.3 ms |
| 終了・削除 | 960.6 ms | 26.0 ms |
| コピー → 起動 → SELECT 1 → 終了・削除 | 1394.2 ms | 474.1 ms |

初回の baseline 準備はそれぞれ 2.652 秒 / 2.727 秒で別計測。
ビルド時間は含めず、OS キャッシュの消去や負荷の隔離はしていない。
測定ログは `.work/disposable-integration.log`。環境によって時間は変わる。
コミット済み変更・未コミット transaction の隔離、baseline の保持、並列実行、
子の SIGKILL・回収・データ削除、Close / Discard の反復を確認した。
