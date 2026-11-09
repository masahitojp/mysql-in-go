# 動作確認

検証日: 2026-11-10。Ubuntu 26.10 / x86_64、GCC 15.3.0、glibc 2.44、CMake 4.3.4、Ninja 1.13.2。

## SQL 機能と永続化の検証

```sh
make build
make test-sql
```

Bao を組み込んだ `bin/mysqlmem` に Go から接続し、以下がすべて成功した。
各クエリーは実行結果を期待値と比較する。SQL エラーを再試行して成功扱いにはしない。

| 検証 | 実測結果 |
| --- | --- |
| CREATE DATABASE / TABLE / INDEX / FOREIGN KEY | InnoDB テーブルを作成 |
| プリペアド INSERT / SELECT / ORDER BY | 3 行を挿入、引用符と SQL 風の文字列もそのまま格納 |
| SUM | 10 + 20 + 0 = 30 |
| ROLLBACK | トランザクション内で 110、取消後は 10 |
| COMMIT | bob の points が 20 → 25 |
| JOIN / GROUP BY | alice:200、bob:50 |
| JSON_SET / JSON 抽出 | Tokyo:3 |
| ROW_NUMBER ウィンドウ関数 | points 降順で bob:1、alice:2、3 行目:3 |
| EXPLAIN | FORCE INDEX 指定で idx_points の range アクセス |
| 主キー重複 | MySQL エラー 1062 |
| 外部キー違反 | MySQL エラー 1452 |
| DELETE | 3 行 → 2 行 |
| 正常終了後に同じデータディレクトリで再起動 | alice:10 / bob:25、JSON visits=3、注文 3 件・合計 250 を保持 |
| 終了処理 | 2 回とも SQL SHUTDOWN で終了コード 0 |

実行ログと結果は、実行時に表示される `.work/sql-*/` に保存する。
再実行ごとに新しい `.work/sql-*` を作るため、既存データには触れない。
これは正常終了後の永続化確認で、強制終了時のクラッシュリカバリーは未検証。
