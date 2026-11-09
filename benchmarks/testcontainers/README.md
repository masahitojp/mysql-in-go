# Testcontainers + Podman との起動時間比較

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
