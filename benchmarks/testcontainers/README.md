# Testcontainers + Podman との起動・終了時間比較

## 最新: 両方式のデータを tmpfs に揃えた比較

2026-11-10、MySQL 8.4.13、Podman 5.8.6 rootless、Testcontainers for Go v0.45.0。
各方式の通常終了（innodb_fast_shutdown=1）と高速終了（2）を、実行順を回転して各 5 回測定した。

| 条件 | 接続可能まで | 停止・片付け | 全体 |
| --- | --- | --- | --- |
| Bao 通常 | 1.718 秒 | 1.022 秒 | 2.741 秒 |
| Bao 高速 | 1.728 秒 | 0.999 秒 | 2.728 秒 |
| Testcontainers 通常 | 5.150 秒 | 1.579 秒 | 6.731 秒 |
| Testcontainers 高速 | 5.308 秒 | 1.451 秒 | 6.762 秒 |

全て平均。小さい DB・5 回では、Bao の高速終了による大きな短縮は確認できなかった。
終了時間は MySQL 全体の停止・プロセス回収・ファイル削除を含み、InnoDB の flush 時間だけではない。
コンテナ側の起動値にも 1 秒程度のばらつきがあり、微差を一般化しない。

[最新の各サンプル JSON](results/tmpfs-shutdown-2026-11-10.json)

### 揃えた条件と残る差

- 毎回新規 DB、buffer pool 64 MiB、X Protocol 無効。
- Bao は検証済みの /dev/shm tmpfs、コンテナも /var/lib/mysql と /tmp を tmpfs に配置。
  コンテナ側の上限はデータ 512 MiB、一時ファイル 64 MiB。
- 両方式とも接続確認と SELECT 1 の assertion、MySQL バージョンと実際の終了モードを検証する。
- イメージ取得・Go ビルド・Ryuk 初回起動は除外。Ryuk は有効。
- TCP と Unix ソケット、コンパイラー設定、公式イメージの entrypoint 初期化手順は異なる。
  差の全てがコンテナのオーバーヘッドという意味ではない。
- 公式イメージへ mode=2 を起動引数で渡すと初期化にも効いて DD Storage Engine の開始に失敗した。
  最新の測定では entrypoint 完了後に SET GLOBAL で 2 にする。
  Bao の Options.FastShutdown も --initialize-insecure コマンドには 2 を渡さない。
- 高速停止はログを flush する一方、次回の起動に crash recovery が必要。
  この比較はデータを毎回破棄するため、再起動時の recovery のコストを含まない。

### 再実行

先にプロジェクトのサーバーをビルドし、Podman socket とイメージを用意する。
比較プログラムは通常の Go API と分離したモジュールに置いている。

```sh
systemctl --user start podman.socket
export DOCKER_HOST="unix://$XDG_RUNTIME_DIR/podman/podman.sock"
podman pull docker.io/library/mysql:8.4.13
make compare-testcontainers
```

現在のデフォルトは 4 条件・各 5 回・両方 tmpfs。
測定バイナリを直接呼ぶ場合も `-container-tmpfs=true` がデフォルト。
`-container-tmpfs=false` ならコンテナの保存先だけを通常の Podman 管理ボリュームに戻せる。
進捗は stderr、サンプルの JSON は stdout。完了時にコンテナが残っていないことも確認した。

---

# 旧比較: 保存先が異なる最初の測定

2026-11-10 に同じ Ubuntu 26.10 / Linux amd64 ホストで測定した。
**初期化を含む Start は Bao 版が約 3.5 倍、終了までの全体では約 2.8 倍短時間だった。**
通常のテストセットアップを対象とする比較であり、SQL 実行速度や Bao だけの効果を示す測定ではない。

| 対象（各5回） | Bao 子プロセス API | Testcontainers + rootless Podman |
| --- | --- | --- |
| 接続可能になるまで | 平均 1.724 秒（1.650〜1.749 秒） | 平均 6.072 秒（5.140〜6.870 秒） |
| 接続後の SELECT 1 | 平均 0.241 ms | 平均 0.135 ms |
| 正常停止・後片付け | 平均 1.004 秒 | 平均 1.532 秒 |
| 全体 | 平均 2.729 秒（2.698〜2.744 秒） | 平均 7.606 秒（6.693〜8.409 秒） |

SQL の 1 回の実行値は微小でサンプル数も少ないため、スループットの優劣は判定しない。
起動部分の差と SQL の処理性能を混同しない。

[測定値 JSON](results/podman-2026-11-10.json) / [測定コード](main.go)

## 条件

- Testcontainers for Go v0.45.0、Podman 5.8.6 rootless。
- MySQL は両方 8.4.13。実行ごとに `SELECT VERSION()` でも検証。
- コンテナは公式 `docker.io/library/mysql:8.4.13`。
  digest: `sha256:72c368d898861f3b3a07af0e6d690400f44f048799ed46822783d9224cc2c86d`。
- 毎回新規データ。両方とも buffer pool 64 MiB、X Protocol 無効。
- ネイティブ版は一時ディレクトリ・Unix ソケット。
  後の確認で、このホストの一時ディレクトリ `/tmp` は **tmpfs** と分かった。
  したがって、この比較では保存先の方式も揃っていない。
  公式イメージは新しい Podman 管理のボリューム・ホストの loopback へ公開する TCP。
  Podman のイメージストレージは overlay。
- root は比較用のパスワードなし設定。ホスト公開ポートは 127.0.0.1 に限定。
- native の Ping / Testcontainers の SQL 待機に加え、接続成功後に SELECT 1 が 1 と一致することを assert。
  どちらも接続待ちのポーリング間隔は 50 ms。
- イメージと Go のビルド時間は除外。Testcontainers を 1 回ウォームアップし、
  Ryuk の初回起動・補助イメージ取得も本測定から除外する。Ryuk は無効化しない。
- 実行順の偏りを減らすため、各方式を交互に測定し、偶数回は順番を逆にする。
- Bao 版は API Close の SQL SHUTDOWN、コンテナは Stop の SIGTERM で正常停止する。
  どちらも終了コード 0 を確認し、子の回収 / コンテナ・ボリューム削除までを終了時間に含める。
- Total は接続確認後のバージョン検証や、コンテナのメタデータ確認も含む。
- OS キャッシュの消去、負荷隔離、コンテナ再利用は行っていない。

公式イメージはネイティブ版と初期化スクリプト・コンパイラー設定・ライブラリ・
保存先・接続方式が異なる。差分全体がコンテナ処理だけの時間という意味ではない。
初期化済み DB の再利用や、テスト群で 1 回だけ起動する場合は別途測る必要がある。

## 再実行

このディレクトリは独立した Go モジュール。通常の API 利用者の依存関係に Testcontainers を追加しない。
リポジトリのルートでネイティブサーバーをビルド済みにし、Podman を導入してから実行する。

```sh
systemctl --user start podman.socket
export DOCKER_HOST="unix://$XDG_RUNTIME_DIR/podman/podman.sock"
podman pull docker.io/library/mysql:8.4.13
make compare-testcontainers
```

測定だけを直接実行し、JSON とログを分ける場合:

```sh
DOCKER_HOST="unix://$XDG_RUNTIME_DIR/podman/podman.sock" \
TESTCONTAINERS_PROVIDER=podman \
./bin/compare-testcontainers -root . -runs 5 \
  > .work/testcontainers-comparison.json \
  2> .work/testcontainers-comparison.log
```

Testcontainers の待機中に `unexpected EOF` がログに出ることがあるが、
SQL 待機はタイムアウトまで継続し、成功後の query/assert や停止エラーは失敗として扱う。
測定用イメージはキャッシュに残り、ユーザーの他のコンテナやイメージは削除しない。

公式手順: [Podman](https://golang.testcontainers.org/system_requirements/using_podman/) /
[MySQL module](https://golang.testcontainers.org/modules/mysql/)
