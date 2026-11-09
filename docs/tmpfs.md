# Disposable な MySQL を tmpfs に置く

Linux の `/dev/shm` が tmpfs であることを検証し、DB データ・制御ファイル・SQL 用一時ファイルをそこに置く。
自動マウントや sudo は不要だが、`/dev/shm` に所有者専用ディレクトリを作成する権限と空き容量が必要。
利用できない場合は明示的にエラーとし、ディスクへの暗黙のフォールバックはしない。

## API

```go
db, err := mysql.Start(mysql.Options{Tmpfs: true})
```

デフォルトのデータは正常な `Close()` で削除する。
指定した DataDir を保持する既存の規約は変えない。
Tmpfs と DataDir を併用する場合は、DataDir 自体も tmpfs 上でなければエラー。
`SELECT @@datadir` と `SELECT @@tmpdir` で実際の配置を確認できる。

fixture でも基準データとテストごとのコピーを tmpfs に置ける。

```go
f, err := mysqltest.New(ctx, mysqltest.Options{
    MySQL: mysql.Options{Tmpfs: true},
    MaxChildren: 2,
    Prepare: prepare,
})
```

fixture は Close / テストの Cleanup で子プロセスを回収し、私有コピーを削除する。
fixture.Close は基準データも削除する。
失敗時に調査用ファイルを残す規約は通常モードと同じ。
親の異常終了時に必ず削除される保証はない。

## 実行テスト

```sh
make test-fixture-tmpfs
```

実サーバーの datadir と SQL tmpdir、fixture の基準ディレクトリの filesystem を確認する。
コミットした変更の隔離、並列実行、容量枠待機のキャンセル、正常終了・削除を検証する。
ディスク上の DataDir が Tmpfs 指定で拒否される異常系も確認する。

この機能は Linux 専用。macOS 向けの RAM ディスク対応は未実装。
`Tmpfs: false` は OS の通常の一時ディレクトリを使う意味で、ディスク利用を保証する設定ではない。

## 今回の実測

2026-11-10 のホストでは **以前から `/tmp` 自体が tmpfs** だった。
そのため通常の一時ディレクトリと明示的な `/dev/shm` の比較は、ディスク対 RAM ではない。

| 小さな seed、5 子の平均 | 従来の `/tmp`（実際は tmpfs） | 明示的な `/dev/shm` tmpfs |
| --- | --- | --- |
| データコピー | 50.7 ms | 49.8 ms |
| 起動・接続待ち | 362.8 ms | 393.9 ms |
| コピー・起動・SELECT 1・終了・削除 | 1.415 秒 | 1.449 秒 |

初回準備は従来 2.706 秒、明示的 tmpfs で 2.741 秒。
別々の実行で負荷隔離もしていないため、微差から速度の優劣は判断しない。
測定ログは `.work/tmpfs-integration.log`。
残っている時間の多くは MySQL の起動・正常終了であり、tmpfs 化による解消は確認できなかった。

tmpfs はメモリーと、設定によっては swap を利用する。ディスクへの書き出しが絶対に起きないという意味ではない。
基準データ＋並列コピー＋MySQL 自体のメモリーの合計を見て、容量と MaxChildren を設定する。
このライブラリは swap 設定やマウント容量を変更しない。
[Linux kernel の tmpfs 説明](https://docs.kernel.org/filesystems/tmpfs.html)
