# mariamem の準備・Fork・片付け時間

2026-11-10、公開 Go モジュール `github.com/masahitojp/mariamem@v0.6.2` を測定。
空の InnoDB テーブルを一つ Prepare し、子ごとに TCP 接続で SELECT 1 を assert、
SELECT VERSION() を記録し、testing.T の Cleanup で子を片付ける。
各 workload を同一プロセスで5回実行した平均。

| workload | 準備 | 子1回（Fork・接続・SQL・片付け） | 最後の baseline 削除 | 全体 |
| --- | --- | --- | --- | --- |
| 1テスト | 67.2 ms | 60.0 ms | 0.0023 ms | **127.2 ms** |
| 5テスト | 68.0 ms | 平均53.4 ms | 0.0028 ms | **335.0 ms** |

[全サンプルと実行環境](results/2026-11-10.json)。実際の DB バージョンは `12.3.3-MariaDB-embedded`。
1テストの最初のサンプルは149.6 ms、5テストの最初のサンプルは408.2 ms。
後続サンプルは同じプロセスのキャッシュを再利用する。

## mysql-in-go / Testcontainers との比較

| 条件 | 1テスト全体 | 5テスト全体 |
| --- | --- | --- |
| mariamem Snapshot/Fork | 0.127秒 | 0.335秒 |
| mysql-in-go tmpfs fixture / Discard | 約3.149秒 | 約4.774秒 |
| Testcontainers + Podman / Stop(timeout=0) | 約5.424秒 | 約27.121秒 |

mysql-in-go は baseline 準備2.731秒＋各子406.2ms＋最後の削除12.0msから算出。
Testcontainers は各回初期化するコンテナの平均5.424秒から算出。
[MySQL 8.4.13 側の測定条件・各サンプル](../testcontainers/README.md)。

同じホスト・カーネル・小さな seed・SQL assertion・片付け込みという workload を合わせた。
ただし **完全な同条件ではない**:

- mariamem は MariaDB 12.3.3、他の2方式は MySQL 8.4.13。実行方式・設定も異なる。
- ホストは Ubuntu 26.10 だが、mariamem の公式サポート・OS判定は Ubuntu 24.04。
  判定を変更せず、Ubuntu 24.04 の rootless Podman コンテナ内で実行した。
  コンテナの作成・起動時間は計測外。CPU・メモリーの制限なし、/tmp は1GiBのtmpfs。
  mysql-in-go はホストで実行した以前の測定なので、userland と測定タイミングが異なる。
- mariamem はデフォルトのTCP接続。mysql-in-go はUnixソケット。
- イメージ取得・ビルド・Goバイナリのプロセス起動は時間に含めない。
  OSキャッシュの消去と負荷隔離は行っていない。
- Snapshot/Fork と停止済みファイルのコピーは実装方式が異なる。
  この値から SQL スループットや大きな seed の性能は判断できない。

## 再実行

リポジトリルートから（Go 1.26.10、Podman が必要）:

```sh
GOCACHE="$PWD/.work/go-cache" GOPATH="$PWD/.work/go-path" CGO_ENABLED=0 \
.work/tools/go/bin/go -C benchmarks/mariamem test -c -o ../../bin/mariamem-timing.test
mkdir -p .work/mariamem-output
podman run --rm --network=none --tmpfs /tmp:rw,size=1g \
  -v "$PWD/bin/mariamem-timing.test:/bench:ro" \
  -v "$PWD/.work/mariamem-output:/out:rw" \
  docker.io/library/ubuntu:24.04 sh -c \
  '/bench -test.v -test.run=TestTiming -test.timeout=10m -children=1; result=$?; cp /tmp/mariamem-timing.json /out/; exit "$result"'
```

`-children=5` で5テスト分を測定する。出力JSONは次の実行で上書きする。
モジュールと依存は go.mod / go.sum に固定し、通常のmysql-in-go利用者には追加しない。
[mariamem の公式 README](https://github.com/masahitojp/mariamem/tree/v0.6.2)
